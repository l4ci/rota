package worker

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/land"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/strutil"
)

// An integration train (`worker train --integration <name>`, #3096) merges its
// members into a pushed integration/r<round>-<name> branch instead of landing
// them: the members merge --no-ff onto the branch's tip, the base merges in
// after them, the tip is pushed before verify (so a red tip still leaves a
// branch to fix on) and a green verify is recorded on the branch's registry
// record. One PR from the branch lands everything later, through the gate.

// integrationNameRe is the name part of an integration branch: one ref
// component, so the branch stays integration/r<round>-<name>.
var integrationNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// integrationOpts refuses the train options an integration train has no
// meaning for: it lands nothing, so it has no green prefix to land and no
// stop-without-diagnosis; a preview pushes nothing, so it has no branch.
func integrationOpts(o TrainOpts) error {
	if o.Integration == "" {
		return nil
	}
	if !integrationNameRe.MatchString(o.Integration) || strings.Contains(o.Integration, "..") || strings.HasSuffix(o.Integration, ".lock") {
		return fail(exitcode.ExitUsage, fmt.Sprintf("--integration %q is not a valid branch name component", o.Integration))
	}
	for flag, set := range map[string]bool{"--land-green": o.LandGreen, "--on-fail stop": o.OnFail == OnFailStop, "--plan": o.Plan} {
		if set {
			return fail(exitcode.ExitUsage, "--integration cannot be combined with "+flag)
		}
	}
	return nil
}

// withIntegrationLock serializes the trains of one integration branch. It is
// not the land lock: an integration train moves no base, so a long verify of
// it never holds up another landing.
func (e Env) withIntegrationLock(ctx context.Context, root, name string, fn func() error) error {
	common, err := rotastate.CommonDirVia(ctx, e.Git, root)
	if err != nil {
		return err
	}
	return fsio.LockedCtx(ctx, rotastate.File(common, "integration-"+name), landLockTimeout, fn)
}

// integrationPlan starts the record of the run: the branch named for the
// round, carrying the earlier record when the branch has one.
func integrationPlan(reg Registry, o TrainOpts, withPR, targets int) (*Integration, error) {
	if withPR != targets {
		return nil, fail(exitcode.ExitUsage, "an integration train merges PRs: every member needs an open PR")
	}
	round, ok := reg.Round()
	if !ok {
		return nil, fail(exitcode.ExitResolution, "no round recorded: an integration branch is named for its round (rota round start)")
	}
	it := Integration{Name: o.Integration, Branch: IntegrationBranch(round, o.Integration), Round: round, Base: o.Base}
	if rec, ok := reg.IntegrationByBranch(it.Branch); ok {
		if rec.Landed {
			return nil, fail(exitcode.ExitRefused, fmt.Sprintf("%s already landed; name a new integration", it.Branch))
		}
		if rec.Base != o.Base {
			return nil, fail(exitcode.ExitRefused, fmt.Sprintf("%s integrates into %s, not %s", it.Branch, rec.Base, o.Base))
		}
		it = rec
	}
	return &it, nil
}

// integrationStart is the commit the scratch tree starts at: the pushed
// branch's tip, else the base. A pushed tip must descend from the tip rota
// recorded, or commits reviewed by the since-range rule could be rewritten.
func (t *trainRun) integrationStart(p *trainPlanning) (string, bool, error) {
	e, it, res := t.e, p.integration, &p.res
	ref := p.rem() + "/" + it.Branch
	tip, code := e.git(t.root, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if code != 0 {
		if it.Assembled != "" {
			done, err := trainBrokeResult(res, fmt.Sprintf("%s is recorded but gone from %s; delete its record or name a new integration", it.Branch, p.rem()))
			return "", done, err
		}
		return p.baseSHA, false, nil
	}
	if it.Assembled == "" {
		return "", true, fail(exitcode.ExitRefused, fmt.Sprintf("%s exists on %s but rota has no record of it; name a new integration", it.Branch, p.rem()))
	}
	if _, code := e.git(t.root, "merge-base", "--is-ancestor", it.Assembled, tip); code != 0 {
		return "", true, fail(exitcode.ExitRefused, fmt.Sprintf("%s at %s does not descend from the tip rota assembled (%s): it was rewritten", it.Branch, strutil.ShortSHA(tip), strutil.ShortSHA(it.Assembled)))
	}
	return tip, false, nil
}

// skipIntegrated drops the members whose pinned head the branch already
// holds; a member whose head moved since its merge stays and merges again.
func (t *trainRun) skipIntegrated(p *trainPlanning, start string) {
	res := &p.res
	var ms []TrainMember
	var heads []string
	for i, m := range res.Members {
		if _, code := t.e.git(t.root, "merge-base", "--is-ancestor", p.heads[i], start); code == 0 {
			res.Notes = append(res.Notes, fmt.Sprintf("INTEGRATED %s — %s is already merged into %s", m.Target, strutil.ShortSHA(p.heads[i]), p.integration.Branch))
			continue
		}
		ms, heads = append(ms, m), append(heads, p.heads[i])
	}
	res.Members, p.heads = ms, heads
}

// assembleIntegration merges the base into the scratch tree after the
// members, pushes the tip to the integration branch and records it.
func (t *trainRun) assembleIntegration(p *trainPlanning) (bool, error) {
	e, ctx, o, res, it := t.e, t.ctx, t.o, &p.res, p.integration
	merges := append([]string{}, p.tips[1:]...)
	run := func(args ...string) (git.Result, error) { return e.Git(ctx, p.scratch, args...) }
	before := p.tips[len(p.tips)-1]
	if err := land.MergeLocal(run, p.baseSHA, land.SyncSubject(o.Base, it.Branch), land.RecoveryGit(ctx, e.Git, p.scratch)); err != nil {
		res.Verdict = GateMergeFailed
		var ce *land.ConflictError
		if errors.As(err, &ce) {
			res.Err = fmt.Sprintf("TRAIN-FAIL integration — merging %s into %s conflicts in: %s", o.Base, it.Branch, strings.Join(conflictPaths(ce.Out), ", "))
			return true, nil
		}
		res.Err, res.Hint = mergeRefusal(err, mergeWords{Failed: fmt.Sprintf("TRAIN-FAIL integration — merging %s into %s in the scratch tree failed", o.Base, it.Branch)})
		return true, nil
	}
	tip, code := e.git(p.scratch, "rev-parse", "HEAD")
	if code != 0 {
		return trainBrokeResult(res, "git rev-parse HEAD failed in the scratch tree")
	}
	if tip != before {
		merges = append(merges, tip)
	}
	p.intTip = tip
	// A plain push: one that is not a fast-forward is refused by the remote,
	// so a fix pushed to the branch since this run started is never lost.
	if out, code := e.runGit(ctx, t.root, "push", "-q", p.rem(), tip+":refs/heads/"+it.Branch); code != 0 {
		res.Verdict = GateBaseMoved
		res.Err = fmt.Sprintf("BASE-MOVED train — pushing %s to %s/%s failed: %s; nothing landed", strutil.ShortSHA(tip), p.rem(), it.Branch, strings.TrimSpace(out))
		res.Hint = "re-run the train: it starts from the branch's new tip"
		return true, nil
	}
	reg, err := LoadRegistry(t.root)
	if err != nil {
		return true, err
	}
	for i, m := range res.Members {
		im := IntegrationMember{Target: m.Target, PR: m.PR, Branch: m.Branch, Head: p.heads[i]}
		if gt, err := reg.GateTargetAny(m.Target); err == nil {
			im.Issue = gt.Issue
		}
		if j := memberIndex(it.Members, m.Target); j >= 0 {
			it.Members[j] = im
		} else {
			it.Members = append(it.Members, im)
		}
	}
	for j, m := range it.Members {
		if _, code := e.git(t.root, "merge-base", "--is-ancestor", m.Head, tip); m.Pending && code == 0 {
			it.Members[j].Pending, it.Members[j].Conflicts = false, nil
		}
	}
	if it.Start == "" {
		it.Start = p.baseSHA
	}
	it.Assembled = tip
	it.RotaMerges = append(it.RotaMerges, merges...)
	it.VerifiedTip, it.VerifiedBase = "", ""
	if err := RecordIntegration(t.root, *it); err != nil {
		return true, err
	}
	rec := *it
	res.Integration = &rec
	t.phase("integration: pushed %s at %s", it.Branch, strutil.ShortSHA(tip))
	return false, nil
}

func memberIndex(ms []IntegrationMember, target string) int {
	for i, m := range ms {
		if m.Target == target {
			return i
		}
	}
	return -1
}

// integrationLand replaces land for an integration train: a verified tip is
// recorded, and nothing lands on the base.
func (t *trainRun) integrationLand(p trainPlan, result TrainResult, prefix trainVerified) (TrainResult, error) {
	res := result.owned()
	if prefix.passing < len(res.Members) {
		return res, nil
	}
	branch := p.integration.Branch
	if err := UpdateIntegration(t.root, branch, func(it *Integration) {
		it.VerifiedTip, it.VerifiedBase = p.intTip, p.baseSHA
	}); err != nil {
		return res, err
	}
	reg, err := LoadRegistry(t.root)
	if err != nil {
		return res, err
	}
	if rec, ok := reg.IntegrationByBranch(branch); ok {
		res.Integration = &rec
	}
	if res.Verdict == "" {
		res.Verdict = GatePass
	}
	res.SHA = strutil.ShortSHA(p.intTip)
	return res, nil
}

// tipOf is the commit verified for the first k members; for every member of
// an integration train it is the pushed tip, which carries the base merge.
func (p trainPlan) tipOf(k int) string {
	if p.integration != nil && k == len(p.heads) {
		return p.intTip
	}
	if k < len(p.tips) {
		return p.tips[k]
	}
	return ""
}

// integrationConflict handles a member that does not merge onto the branch:
// it is no culprit. The members before it are pushed as usual, the member is
// recorded as pending with the paths, and the train answers interaction, so
// the CLI files the item a worker resolves on the branch. Nothing is verified.
func (t *trainRun) integrationConflict(p *trainPlanning, failed int, paths []string) (bool, error) {
	res, it := &p.res, p.integration
	pending := res.Members[failed]
	pendingHead := p.heads[failed]
	res.Members, p.heads, p.tips = res.Members[:failed], p.heads[:failed], p.tips[:failed+1]
	if done, err := t.assembleIntegration(p); done || err != nil {
		return done, err
	}
	im := IntegrationMember{Target: pending.Target, PR: pending.PR, Branch: pending.Branch, Head: pendingHead, Pending: true, Conflicts: paths}
	if reg, err := LoadRegistry(t.root); err == nil {
		if gt, err := reg.GateTargetAny(pending.Target); err == nil {
			im.Issue = gt.Issue
		}
	}
	if j := memberIndex(it.Members, im.Target); j >= 0 {
		it.Members[j] = im
	} else {
		it.Members = append(it.Members, im)
	}
	if err := RecordIntegration(t.root, *it); err != nil {
		return true, err
	}
	rec := *it
	res.Integration = &rec
	res.Interaction = append([]string{pending.Target}, t.touching(p, it.Members, pending.Target, paths)...)
	res.Verdict, res.Culprit = GateInteraction, ""
	res.Err = fmt.Sprintf("TRAIN-INTERACTION %s — %s does not merge onto %s: conflict in %s; the members before it are pushed, nothing is verified or landed",
		strings.Join(res.Interaction, ", "), pending.Branch, it.Branch, strings.Join(paths, ", "))
	res.Hint = fmt.Sprintf("a worker merges %s into %s and resolves the conflict there, then re-run the train; bounce neither", pending.Branch, it.Branch)
	return true, nil
}

// touching names the merged members whose change touches one of paths, the
// members a conflicting member collides with.
func (t *trainRun) touching(p *trainPlanning, ms []IntegrationMember, except string, paths []string) []string {
	var out []string
	for _, m := range ms {
		if m.Target == except || m.Pending {
			continue
		}
		names, code := t.e.git(t.root, "diff", "--name-only", p.baseSHA+"..."+m.Head)
		if code != 0 {
			continue
		}
		for _, f := range strings.Split(names, "\n") {
			if slices.Contains(paths, strings.TrimSpace(f)) {
				out = append(out, m.Target)
				break
			}
		}
	}
	return out
}
