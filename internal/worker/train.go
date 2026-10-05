package worker

import (
	"context"
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/land"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Merge train (#83): verify several queued PRs together once, then land them.
//
// One serial gate is the slowest step of a round (#46), and N PRs gated one at a
// time pay it N times. A train pays it once. In order:
//
//  1. CHECK each target the way `gate --check-only` does (freshness, PR
//     identity, provenance). Any refusal stops the train, naming that target.
//  2. APPROVE once for the whole train (B1 merge approval over the union of the
//     files the members change), before the expensive step.
//  3. MERGE the members in order onto the base in a scratch worktree. A conflict
//     names the member that does not fit on the base plus the members before it.
//  4. VERIFY the scratch tree once with refactor.verifyCommands.
//  5. On a pass LAND every member through the ordinary gate (forge merge, pinned
//     to the verified head), in order, without re-verifying. Before the first
//     landing the base and every head must still be what the scratch tree was
//     built from; otherwise nothing lands.
//  6. On a fail BISECT: verify growing prefixes of the train to find the first
//     member whose addition breaks the tree and name it. That is the member to
//     send back; it can be an interaction with the members before it, not only
//     the member alone. With LandGreen the verified prefix before it lands.

// GateBaseMoved: the base or a member's head moved while the train verified, so
// the verified tree is no longer what would land. Nothing landed.
const GateBaseMoved = "base-moved"

// TrainOpts are the flags of `rota worker train`.
type TrainOpts struct {
	Targets []string // slot names or PR refs, in merge order
	Base    string
	// LandGreen lands the verified prefix before the culprit when the train
	// fails verification.
	LandGreen bool
	// Approve is GateOpts.Approve for the whole train: files lists the union of
	// the paths the members change.
	Approve func(files func() ([]string, error)) error
}

// TrainMember is one target of the train, in order.
type TrainMember struct {
	Target  string
	Branch  string
	PR      string
	Landed  bool
	Culprit bool
}

// TrainResult is the train's answer. Verdict is pass, or the verdict of the
// step that refused: a member's check verdict (stale, pr-mismatch,
// provenance-fail, ...), merge-failed, verify-failed, approval-required,
// base-moved. Culprit names the member the verdict is about, when one.
type TrainResult struct {
	Base     string
	Verdict  string
	Members  []TrainMember
	Culprit  string
	Verified []string
	Landed   []string
	Changed  bool
	SHA      string
	Err      string
	Hint     string
	Notes    []string
}

// OK reports a train that verified and landed whole.
func (r TrainResult) OK() bool { return r.Verdict == GatePass }

// Train runs a merge train (see above). The base must be checked out in root.
func (e Env) Train(ctx context.Context, root string, o TrainOpts) (TrainResult, error) {
	e = e.withDefaults()
	res := TrainResult{Base: o.Base}
	if len(o.Targets) == 0 {
		return res, fail(exitcode.ExitUsage, "a train needs at least one PR or slot")
	}
	reg := LoadRegistry(root)
	if !reg.Exists {
		return res, fail(exitcode.ExitResolution, "no worker pool — run rota worker pool init first")
	}
	if _, code := e.git(root, "rev-parse", "--verify", "--quiet", o.Base); code != 0 {
		return res, fail(exitcode.ExitResolution, fmt.Sprintf("base branch '%s' does not exist", o.Base))
	}
	if cur, _ := e.git(root, "rev-parse", "--abbrev-ref", "HEAD"); cur != o.Base {
		return res, fail(exitcode.ExitResolution, fmt.Sprintf("a train must run with %s checked out (currently on %s)", o.Base, cur))
	}
	seen := map[string]bool{}
	withPR := 0
	for _, t := range o.Targets {
		gt, err := reg.GateTarget(t)
		if err != nil {
			return res, err
		}
		key := gt.Branch + "|" + gt.PR
		if seen[key] {
			return res, fail(exitcode.ExitUsage, fmt.Sprintf("%s names a PR already in the train", t))
		}
		seen[key] = true
		if gt.PR != "" {
			withPR++
		}
	}
	if withPR != 0 && withPR != len(o.Targets) {
		return res, fail(exitcode.ExitUsage, fmt.Sprintf("a train is all PRs or all slots without one, not a mix: PRs merge onto origin/%s and slots onto the local %s", o.Base, o.Base))
	}

	// 1. Check every member.
	remote := false
	for _, t := range o.Targets {
		gr, err := e.Gate(ctx, root, GateOpts{Slot: t, Base: o.Base, CheckOnly: true})
		if err != nil {
			return res, err
		}
		res.Notes = append(res.Notes, gr.Notes...)
		if !gr.OK() {
			res.Verdict, res.Culprit, res.Err, res.Hint = gr.Verdict, t, gr.Err, gr.Hint
			return res, nil
		}
		res.Members = append(res.Members, TrainMember{Target: t, Branch: gr.Branch, PR: gr.PR})
		remote = remote || gr.PR != ""
	}
	baseRef := o.Base
	if remote {
		baseRef = "origin/" + o.Base
	}
	headRef := func(m TrainMember) string {
		if m.PR != "" {
			return "origin/" + m.Branch
		}
		return m.Branch
	}

	// 2. One approval for the train.
	if o.Approve != nil {
		files := func() ([]string, error) {
			set := map[string]bool{}
			for _, m := range res.Members {
				out, code := e.git(root, "diff", "--name-only", baseRef+"..."+headRef(m))
				if code != 0 {
					return nil, fmt.Errorf("git diff --name-only %s...%s exited %d", baseRef, headRef(m), code)
				}
				for _, l := range strings.Split(out, "\n") {
					if l != "" {
						set[l] = true
					}
				}
			}
			list := make([]string, 0, len(set))
			for f := range set {
				list = append(list, f)
			}
			sort.Strings(list)
			return list, nil
		}
		if err := o.Approve(files); err != nil {
			res.Verdict = GateApprovalRequired
			return res, err
		}
	}

	// 3. Merge in order in a scratch worktree.
	baseSHA, code := e.git(root, "rev-parse", baseRef)
	if code != 0 {
		return e.trainBroke(res, fmt.Sprintf("git rev-parse %s exited %d", baseRef, code))
	}
	heads := make([]string, len(res.Members))
	for i, m := range res.Members {
		if heads[i], code = e.git(root, "rev-parse", headRef(m)); code != 0 {
			return e.trainBroke(res, fmt.Sprintf("git rev-parse %s exited %d", headRef(m), code))
		}
	}
	scratch, err := os.MkdirTemp("", "rota-train-")
	if err != nil {
		return res, err
	}
	scratch = filepath.Join(scratch, "tree")
	if out, code := e.git(root, "worktree", "add", "--detach", scratch, baseSHA); code != 0 {
		return e.trainBroke(res, "could not create the scratch worktree: "+out)
	}
	defer func() {
		e.git(root, "worktree", "remove", "--force", scratch)
		os.RemoveAll(filepath.Dir(scratch))
		e.git(root, "worktree", "prune")
	}()
	tips := make([]string, len(res.Members)+1) // tips[i]: the scratch tree with the first i members merged
	tips[0] = baseSHA
	for i, m := range res.Members {
		run := func(args ...string) (git.Result, error) { return e.Git(ctx, scratch, args...) }
		if merr := land.MergeLocal(run, heads[i], fmt.Sprintf("train: %s into %s", m.Branch, o.Base)); merr != nil {
			res.Culprit = m.Target
			res.Members[i].Culprit = true
			res.Verdict = GateMergeFailed
			var me *land.MergeError
			switch {
			case errors.As(merr, new(*land.ConflictError)):
				res.Err = fmt.Sprintf("TRAIN-FAIL %s — %s does not merge onto %s with the %d member(s) before it: conflict", m.Target, m.Branch, o.Base, i)
				res.Hint = fmt.Sprintf("send %s back to merge %s, or run the train without it", m.Target, o.Base)
			case errors.As(merr, &me):
				res.Err = fmt.Sprintf("TRAIN-FAIL %s — merging %s into the scratch tree failed (exit %d): %s", m.Target, m.Branch, me.Code, strings.TrimSpace(me.Out))
			default:
				res.Err = fmt.Sprintf("TRAIN-FAIL %s — merging %s into the scratch tree failed (exit 127): %s", m.Target, m.Branch, merr)
			}
			return res, nil
		}
		var code int
		if tips[i+1], code = e.git(scratch, "rev-parse", "HEAD"); code != 0 {
			return e.trainBroke(res, "git rev-parse HEAD failed in the scratch tree")
		}
	}

	// 4. Verify once.
	n := len(res.Members)
	passing := n
	vr, err := e.Verify(ctx, root, scratch)
	if err != nil {
		return res, err
	}
	if vr.NoCommands {
		res.Notes = append(res.Notes, "NO-VERIFY train — refactor.verifyCommands is empty; the merged tree was NOT gated by a command.",
			"set refactor.verifyCommands via rota config set to make this gate real")
	} else {
		failLog := vr.LogPath
		res.Verified = vr.Verified
		for _, c := range vr.Failed {
			res.Notes = append(res.Notes, "verify FAILED: "+c)
		}
		if !vr.OK() {
			// 6. Bisect: the first prefix that fails ends in the culprit.
			lo, hi := 0, n
			for hi-lo > 1 {
				mid := (lo + hi) / 2
				if _, code := e.git(scratch, "checkout", "-q", "--detach", tips[mid]); code != 0 {
					return e.trainBroke(res, "git checkout "+tips[mid]+" failed in the scratch tree")
				}
				pr, err := e.Verify(ctx, root, scratch)
				if err != nil {
					return res, err
				}
				if pr.OK() {
					lo = mid
				} else {
					os.Remove(failLog)
					failLog, hi = pr.LogPath, mid
				}
			}
			if lo == 0 { // bisect assumes a green base; the first member only looks guilty on a red one
				if _, code := e.git(scratch, "checkout", "-q", "--detach", tips[0]); code != 0 {
					return e.trainBroke(res, "git checkout "+tips[0]+" failed in the scratch tree")
				}
				br, err := e.Verify(ctx, root, scratch)
				if err != nil {
					return res, err
				}
				if !br.OK() {
					os.Remove(failLog)
					res.Verdict = GateVerifyFailed
					res.Err = fmt.Sprintf("TRAIN-FAIL base — %s fails verification on its own, so no member can be blamed\nlast lines of the verify output (full log: %s):\n%s",
						baseRef, br.LogPath, indentTail(br.Log, 20))
					res.Hint = fmt.Sprintf("fix %s, then re-run the train", baseRef)
					return res, nil
				}
			}
			c := res.Members[hi-1]
			res.Members[hi-1].Culprit = true
			res.Culprit, res.Verdict = c.Target, GateVerifyFailed
			b, _ := os.ReadFile(failLog)
			res.Err = fmt.Sprintf("TRAIN-FAIL %s — the train fails verification once %s (%s) is merged; the first %d member(s) pass\nlast lines of the verify output (full log: %s):\n%s",
				c.Target, c.Target, c.Branch, lo, failLog, indentTail(string(b), 20))
			res.Hint = fmt.Sprintf("send %s back, or run the train without it; it may break only with the member(s) before it", c.Target)
			if !o.LandGreen || lo == 0 {
				return res, nil
			}
			passing = lo
			res.Hint += fmt.Sprintf("; the verified first %d member(s) landed (--land-green)", lo)
		}
	}

	// 5. Land. The tree that was verified must still be the one that lands.
	if remote {
		if _, code := e.git(root, "fetch", "origin", "-q"); code != 0 {
			return e.trainBroke(res, "git fetch origin failed before landing")
		}
	}
	verdict, culprit := res.Verdict, res.Culprit
	if cur, _ := e.git(root, "rev-parse", baseRef); cur != baseSHA {
		return e.trainMoved(res, "", fmt.Sprintf("%s moved from %s to %s while the train verified", baseRef, short(baseSHA), short(cur)))
	}
	for i := 0; i < passing; i++ {
		if cur, _ := e.git(root, "rev-parse", headRef(res.Members[i])); cur != heads[i] {
			return e.trainMoved(res, res.Members[i].Target, fmt.Sprintf("%s moved from %s to %s while the train verified", headRef(res.Members[i]), short(heads[i]), short(cur)))
		}
	}
	for i := 0; i < passing; i++ {
		m := res.Members[i]
		if i > 0 { // each landing must start from exactly the tree the scratch merge had at that point
			if remote {
				if _, code := e.git(root, "fetch", "origin", "-q"); code != 0 {
					return e.trainBroke(res, "git fetch origin failed between landings")
				}
			}
			got, _ := e.git(root, "rev-parse", baseRef+"^{tree}")
			if want, _ := e.git(root, "rev-parse", tips[i]+"^{tree}"); got != want {
				return e.trainMoved(res, m.Target, fmt.Sprintf("%s changed outside the train after %d landing(s) (tree %s, verified %s)", baseRef, i, short(got), short(want)))
			}
		}
		gr, err := e.Gate(ctx, root, GateOpts{Slot: m.Target, Base: o.Base, NoVerify: true, Train: true})
		res.Notes = append(res.Notes, gr.Notes...)
		res.Changed = res.Changed || gr.Changed
		if gr.Changed && gr.Verdict != GatePass { // the PR is on the base but the gate could not finish (merged-remotely)
			res.Landed = append(res.Landed, m.Target)
			res.Members[i].Landed = true
		}
		if err != nil {
			return res, err
		}
		if !gr.OK() {
			res.Verdict, res.Culprit, res.Err, res.Hint = gr.Verdict, m.Target, gr.Err, gr.Hint
			res.Hint = strings.TrimSpace(fmt.Sprintf("landed %d of %d member(s) before this; %s", len(res.Landed), n, res.Hint))
			return res, nil
		}
		res.Landed = append(res.Landed, m.Target)
		res.Members[i].Landed = true
	}
	res.SHA, _ = e.git(root, "rev-parse", "--short=7", "HEAD")
	if tree, _ := e.git(root, "rev-parse", "HEAD^{tree}"); tree != "" {
		if want, _ := e.git(root, "rev-parse", tips[passing]+"^{tree}"); want != tree {
			return e.trainMoved(res, "", fmt.Sprintf("the landed tree %s differs from the verified scratch tree %s; the forge merged differently than git did", short(tree), short(want)))
		}
	}
	res.Verdict, res.Culprit = verdict, culprit
	if verdict == "" {
		res.Verdict = GatePass
	}
	return res, nil
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func (e Env) trainBroke(res TrainResult, msg string) (TrainResult, error) {
	res.Verdict, res.Err = GateCheckBroke, "CHECK-BROKE train — "+msg
	return res, nil
}

func (e Env) trainMoved(res TrainResult, culprit, msg string) (TrainResult, error) {
	res.Verdict, res.Culprit = GateBaseMoved, culprit
	if len(res.Landed) == 0 {
		res.Err = "BASE-MOVED train — " + msg + "; nothing landed"
		res.Hint = "re-run the train on the new base"
		return res, nil
	}
	res.Err = fmt.Sprintf("BASE-MOVED train — %s; landed %d of %d member(s): %s", msg, len(res.Landed), len(res.Members), strings.Join(res.Landed, ", "))
	res.Hint = fmt.Sprintf("landed %d of %d member(s); re-run the train for the rest on the new base", len(res.Landed), len(res.Members))
	return res, nil
}
