package worker

import (
	"context"
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/landing"
	"strings"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/land"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/tracker"
)

// Stacked integration branches (#3098): `worker train --integration b --onto
// a` builds b on open integration branch a and aims b's PR at it. While a is
// open, b waits; once a lands, b's PR returns to a's base, that base merges
// into b and b's verify stands unless the merge changed b's tree.

// prRetargeter is the optional Forge capability of aiming a PR at another
// branch (tracker.Forge has it for GitHub and GitLab).
type prRetargeter interface {
	PRSetBase(ctx context.Context, pr int, base string) error
}

// stackRefusal is why a stacked branch cannot land yet.
type stackRefusal struct{ verdict, msg, hint string }

// settleStack decides a stacked integration's landing, under its integration
// lock. With its base branch landed it moves onto the base (a retry of what the
// landing of that branch did, or the whole of it when that was skipped); with
// the base still open or closed unmerged it refuses, naming the base branch.
func (e Env) settleStack(ctx context.Context, root string, rec landing.Integration) (landing.Integration, []string, *stackRefusal) {
	reg, err := LoadRegistry(root)
	if err != nil {
		return rec, nil, &stackRefusal{landing.GateCheckBroke, "cannot read the integration records: " + err.Error(), "gate the PR again"}
	}
	under, ok := reg.IntegrationByBranch(rec.Onto)
	if !ok {
		return rec, nil, &stackRefusal{landing.GateStale, fmt.Sprintf("its base %s has no record", rec.Onto), "delete the record of " + rec.Branch + " and train it again"}
	}
	if under.Landed {
		moved, note, err := e.restack(ctx, root, rec)
		if err != nil {
			return rec, nil, &stackRefusal{landing.GateStale, fmt.Sprintf("%s landed but %s cannot move onto %s: %s", rec.Onto, rec.Branch, rec.OntoBase, err), "fix that, then gate the PR again"}
		}
		return moved, []string{note}, nil
	}
	if under.PR != "" {
		if in, err := e.GateInput(root); err == nil {
			if f, _, refusal := e.openForge(ctx, root, under.PR, in.cfg, tracker.ForgeActor); refusal == nil {
				if info, err := f.PRView(ctx, prNumberOf(under.PR)); err == nil && strings.EqualFold(info.State, "CLOSED") {
					return rec, nil, &stackRefusal{landing.GateStale, fmt.Sprintf("its base %s was closed without landing (PR %s)", under.Branch, under.PR),
						"land " + under.Branch + " some other way and train this branch onto " + rec.OntoBase + ", or drop it"}
				}
			}
		}
	}
	return rec, nil, &stackRefusal{landing.GateStale, fmt.Sprintf("it is stacked on %s, which has not landed", under.Branch), "land " + under.Branch + " first: this PR then retargets to " + rec.OntoBase}
}

func prNumberOf(pr string) int {
	n, _ := PRRefNumber(pr)
	return n
}

// restack moves a stacked integration off the branch that landed: the base
// merges into it and is pushed, its PR is retargeted, and the record drops the
// stack. The verify of the branch stands when that merge left its tree as the
// verified one. It is safe to run again after a failure: each step is
// idempotent and the record changes last.
func (e Env) restack(ctx context.Context, root string, rec landing.Integration) (landing.Integration, string, error) {
	in, err := e.GateInput(root)
	if err != nil {
		return rec, "", err
	}
	rem := in.cfg.RemoteName()
	if out, code := e.runGit(ctx, root, "fetch", "-q", "--prune", rem); code != 0 {
		return rec, "", fmt.Errorf("git fetch %s failed: %s", rem, strings.TrimSpace(out))
	}
	tip, code := e.git(root, "rev-parse", "--verify", "--quiet", rem+"/"+rec.Branch+"^{commit}")
	if code != 0 {
		return rec, "", fmt.Errorf("%s is gone from %s", rec.Branch, rem)
	}
	base, code := e.git(root, "rev-parse", "--verify", "--quiet", rem+"/"+rec.OntoBase+"^{commit}")
	if code != 0 {
		return rec, "", fmt.Errorf("%s is gone from %s", rec.OntoBase, rem)
	}
	merged, err := e.mergeBaseInto(ctx, root, rem, rec, tip, base)
	if err != nil {
		return rec, "", err
	}
	if rec.PR != "" {
		f, _, refusal := e.openForge(ctx, root, rec.PR, in.cfg, tracker.ForgeActor)
		if refusal != nil {
			return rec, "", refusal.err
		}
		rt, ok := f.(prRetargeter)
		if !ok {
			return rec, "", errors.New("the forge cannot retarget a PR")
		}
		if err := rt.PRSetBase(ctx, prNumberOf(rec.PR), rec.OntoBase); err != nil {
			return rec, "", fmt.Errorf("retargeting %s onto %s: %w", rec.PR, rec.OntoBase, err)
		}
	}
	same := false
	if rec.VerifiedTip != "" {
		old, c1 := e.git(root, "rev-parse", "--verify", "--quiet", rec.VerifiedTip+"^{tree}")
		cur, c2 := e.git(root, "rev-parse", "--verify", "--quiet", merged+"^{tree}")
		same = c1 == 0 && c2 == 0 && old == cur
	}
	mergesOfBase := e.baseMerges(root, rec, merged, base)
	err = UpdateIntegration(root, rec.Branch, func(it *landing.Integration) {
		it.Base, it.Onto, it.OntoBase = rec.OntoBase, "", ""
		it.Assembled = merged
		for _, c := range mergesOfBase {
			if !it.IsRotaMerge(c) {
				it.RotaMerges = append(it.RotaMerges, c)
			}
		}
		if same {
			it.VerifiedTip, it.VerifiedBase = merged, base
		} else {
			it.VerifiedTip, it.VerifiedBase = "", ""
		}
	})
	if err != nil {
		return rec, "", err
	}
	reg, err := LoadRegistry(root)
	if err != nil {
		return rec, "", err
	}
	out, _ := reg.IntegrationByBranch(rec.Branch)
	e.refreshStackedBody(ctx, root, in.cfg, out)
	verdict := "its tree changed, so it verifies again"
	if same {
		verdict = "its tree is unchanged, so its verify stands"
	}
	return out, fmt.Sprintf("STACK-RETARGET %s — %s landed; the PR now targets %s, which is merged in; %s", rec.Branch, rec.Onto, rec.OntoBase, verdict), nil
}

// baseMerges are the first-parent merges between the assembled tip and the new
// tip that take in the base: a merge whose second parent the base holds. Pushed
// by this attempt or an earlier one that failed before the record changed, they
// are rota's own, never fixes for a review. A fix is never a merge of the base.
func (e Env) baseMerges(root string, rec landing.Integration, merged, base string) []string {
	out, code := e.git(root, "rev-list", "--first-parent", "--merges", "--parents", rec.Assembled+".."+merged)
	if code != 0 {
		return []string{merged}
	}
	var found []string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) != 3 {
			continue
		}
		// A best-effort listing: a ref git cannot read is left out, like one not on base.
		if onBase, err := e.isAncestor(root, f[2], base); err == nil && onBase {
			found = append(found, f[0])
		}
	}
	return found
}

// refreshStackedBody rewrites the PR body of a branch that left its stack, so it
// names the base it now lands on. A failure is of no weight: the body is text.
func (e Env) refreshStackedBody(ctx context.Context, root string, cfg GateConfig, rec landing.Integration) {
	if rec.PR == "" {
		return
	}
	f, _, refusal := e.openForge(ctx, root, rec.PR, cfg, tracker.ForgeActor)
	if refusal != nil {
		return
	}
	if s, ok := f.(interface {
		PRSetBody(ctx context.Context, pr int, body string) error
	}); ok {
		_ = s.PRSetBody(ctx, prNumberOf(rec.PR), IntegrationPRBody(rec))
	}
}

// mergeBaseInto merges base into the integration branch's tip in a scratch
// tree and pushes the result; it answers the new tip (tip itself when the
// branch already holds base). A plain push: a fix pushed meanwhile is never lost.
func (e Env) mergeBaseInto(ctx context.Context, root, rem string, rec landing.Integration, tip, base string) (string, error) {
	scratch, cleanup, err := e.scratchTree(ctx, root, tip, "rota-restack-")
	if err != nil {
		return "", err
	}
	defer cleanup()
	run := func(args ...string) (git.Result, error) { return e.Git(ctx, scratch, args...) }
	merged, err := land.LandLocal(land.LocalPorts{Git: run, Recover: land.RecoveryGit(ctx, e.Git, scratch)},
		land.LocalRequest{Head: base, Base: "scratch tree", Message: land.SyncSubject(rec.OntoBase, rec.Branch)})
	if err != nil {
		if errors.As(err, new(*land.PostMergeError)) {
			return "", err
		}
		return "", mergeBaseError(rec, err)
	}
	if merged == tip {
		return tip, nil
	}
	if out, code := e.runGit(ctx, root, "push", "-q", rem, merged+":refs/heads/"+rec.Branch); code != 0 {
		return "", fmt.Errorf("pushing %s to %s/%s failed: %s", strutil.ShortSHA(merged), rem, rec.Branch, strings.TrimSpace(out))
	}
	return merged, nil
}

// chainedError words a failure for the operator and keeps the cause for errors.Is/As.
type chainedError struct {
	msg string
	err error
}

func (e *chainedError) Error() string { return e.msg }
func (e *chainedError) Unwrap() error { return e.err }

// mergeBaseError words a failed base merge and keeps the error chain. Only an
// error that carries a git exit code gets "(exit N)": an error that is not a
// git exit (ErrUnpinned, a runner failure) is worded without one.
func mergeBaseError(rec landing.Integration, err error) error {
	failed := fmt.Sprintf("merging %s into %s failed", rec.OntoBase, rec.Branch)
	f := land.Classify(err)
	if f.Kind == land.FailOther {
		return &chainedError{msg: fmt.Sprintf("%s: %v", failed, err), err: err}
	}
	msg, _ := f.Refusal(land.Words{
		Failed:   failed,
		Conflict: fmt.Sprintf("merging %s into %s conflicts in: {paths}", rec.OntoBase, rec.Branch),
	})
	return &chainedError{msg: msg, err: err}
}

// restackDependents runs after an integration branch landed: each open
// branch stacked on it moves onto its base. A failure is a note, never a
// failure of the landing that already happened; the dependent's own gate
// repeats the move. It runs outside the landed branch's locks, under each
// dependent's own integration lock.
func (e Env) restackDependents(ctx context.Context, root string, landed landing.Integration) []string {
	reg, err := LoadRegistry(root)
	if err != nil {
		return []string{"STACK-RETARGET not run — cannot read the registry: " + err.Error()}
	}
	var notes []string
	for _, dep := range reg.Integrations() {
		if dep.Landed || dep.Onto != landed.Branch {
			continue
		}
		err := e.WithIntegrationLock(ctx, root, dep.Name, func() error {
			cur, _ := LoadRegistry(root)
			rec, ok := cur.IntegrationByBranch(dep.Branch)
			if !ok || rec.Onto != landed.Branch {
				return nil
			}
			_, note, err := e.restack(ctx, root, rec)
			if err == nil {
				notes = append(notes, note)
			}
			return err
		})
		if err != nil {
			notes = append(notes, fmt.Sprintf("STACK-RETARGET %s pending — %s; gating its PR retries it", dep.Branch, err))
		}
	}
	return notes
}
