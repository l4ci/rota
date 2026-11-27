package worker

import (
	"cmp"
	"fmt"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/landing"
	"github.com/l4ci/rota/internal/overlap"
	"github.com/l4ci/rota/internal/tracker"
	"strings"
)

// stepFreshness is stage 1. Three-way on the exit code: `&& FRESH || STALE`
// would call a check that itself errors STALE.
func (runner gateRun) stepFreshness(result landing.GateResult, target GateTarget, forged gateForgeResult, remote bool, refs gateRefsResult) (struct{}, landing.GateResult, bool, error) {
	g := runner.operation(result, target, forged)
	g.remote = remote
	g.headRef, g.baseRef, g.verified = refs.head, refs.base, refs.localHead
	finish := func(done bool, err error) (struct{}, landing.GateResult, bool, error) {
		return struct{}{}, *g.res, done, err
	}

	o := g.o
	upToDate, err := g.isAncestor(g.root, g.baseRef, g.headRef)
	if err != nil {
		return finish(g.broke(err.Error()))
	}
	if upToDate {
		return finish(false, nil)
	}
	behind, c := g.git(g.root, "rev-list", "--count", g.headRef+".."+g.baseRef)
	if c != 0 {
		behind = "?"
	}
	var why, brokeMsg string
	var shared []string
	if !o.LandVerified { // a train already merged every PR cleanly, in order, in its scratch tree
		why, shared, brokeMsg = g.staleReason()
	}
	if brokeMsg != "" {
		return finish(g.broke(brokeMsg))
	}
	if why != "" {
		g.noteMergedRemotely()
		g.res.SHA, _ = g.git(g.root, "rev-parse", "--short=7", g.headRef) // bounce accounting keys on the head
		owner := "the worker on branch " + g.branch
		if target.Name != "" {
			owner = "slot " + target.Name
		}
		g.verdict(landing.GateStale, fmt.Sprintf("STALE %s %s — %s commit(s) landed on %s since it branched; %s", o.Slot, g.branch, behind, o.Base, why),
			fmt.Sprintf("bounce: tell %s to `git merge %s`, resolve and re-verify, then re-gate", owner, o.Base))
		return finish(true, nil)
	}
	if !o.LandVerified {
		detail := "none touch its files and the merge is clean, merging as is"
		if len(shared) > 0 {
			detail = "both sides changed " + strings.Join(shared, ", ") + " but the merge is clean, merging and verifying the merged tree"
		}
		g.res.Notes = append(g.res.Notes, fmt.Sprintf("STALE-MERGE %s — %s commit(s) landed on %s since %s branched; %s", o.Slot, behind, o.Base, g.branch, detail))
	}
	return finish(false, nil)
}

// stepPRMatches checks the PR is the thing that was just verified: open, aimed
// at the gate's base (a PR stacked on another worker's branch merges THERE, not
// here), and headed by the pushed commit.
func (runner gateRun) stepPRMatches(result landing.GateResult, target GateTarget, forged gateForgeResult, remote bool, refs gateRefsResult, viewed *tracker.PRInfo) (string, landing.GateResult, bool, error) {
	g := runner.operation(result, target, forged)
	g.remote = remote
	g.headRef, g.baseRef, g.verified = refs.head, refs.base, refs.localHead
	g.viewed = viewed
	finish := func(done bool, err error) (string, landing.GateResult, bool, error) {
		return g.verified, *g.res, done, err
	}

	if !g.remote {
		return finish(false, nil)
	}
	var code int
	if g.verified, code = g.git(g.root, "rev-parse", g.headRef); code != 0 || g.verified == "" {
		return finish(g.broke(fmt.Sprintf("git rev-parse %s failed (exit %d)", g.headRef, code)))
	}
	info, ok := g.viewed, true
	g.viewed = nil
	if info == nil {
		var v tracker.PRInfo
		if v, ok = g.prInfo(); ok {
			info = &v
		}
	}
	if !ok {
		return finish(g.broke(fmt.Sprintf("could not read PR %s from %s", g.prNum, g.provider)))
	}
	mismatch := func(msg string) (bool, error) {
		g.verdict(landing.GatePRMismatch, "error: "+msg, "")
		return true, nil
	}
	// OPEN, MERGED or CLOSED on both forges.
	switch {
	case info.State != "OPEN":
		g.res.AlreadyMerged = info.State == "MERGED"
		return finish(mismatch(fmt.Sprintf("PR %s is %s, not open", g.prNum, info.State)))
	case info.Base != cmp.Or(g.o.PRBase, g.o.Base):
		return finish(mismatch(fmt.Sprintf("PR %s targets '%s', the gate's base is '%s' — stacked PR?", g.prNum, info.Base, cmp.Or(g.o.PRBase, g.o.Base))))
	case info.Head != g.branch:
		return finish(mismatch(fmt.Sprintf("PR %s is headed by '%s', slot %s verified '%s'", g.prNum, info.Head, g.o.Slot, g.branch)))
	}
	// The forge can lag a push: origin has the commit and the PR still names the
	// old head. Read again a bounded number of times before calling it a mismatch.
	for i := 1; info.HeadSHA != g.verified && i < gateHeadTries; i++ {
		g.e.Sleep(gateHeadWait)
		v, ok := g.prInfo()
		if !ok {
			return finish(g.broke(fmt.Sprintf("could not re-read PR %s from %s", g.prNum, g.provider)))
		}
		info = &v
		if info.State != "OPEN" || info.Head != g.branch {
			break
		}
	}
	if info.HeadSHA != g.verified {
		g.verdict(landing.GatePRMismatch,
			fmt.Sprintf("error: PR %s head is %s, the verified %s is %s — pushed since, or %s has not caught up with the push (read %d times)", g.prNum, info.HeadSHA, g.headRef, g.verified, g.provider, gateHeadTries),
			fmt.Sprintf("once the PR shows the pushed head, run `rota worker gate %s` again without --request-id: the same id replays this refusal", g.o.Slot))
		return finish(true, nil)
	}
	return finish(false, nil)
}

// pairwisePaths is the shared footprint check for train ordering, conflict
// attribution, preview pruning and stale-merge notes. Only the gate filters
// round.sharedPaths; the train passes nil so it sees every shared path.
func pairwisePaths(a, b, shared []string) []string {
	return overlap.Both(a, b, shared)
}

// pairwiseMergeConflict distinguishes a real conflict from a broken git probe.
// A non-zero failedCode means the probe failed; output is kept for diagnostics.
// Callers choose whether to prune disjoint paths first: the gate probes even
// disjoint footprints, since renames can conflict without a shared path.
func pairwiseMergeConflict(runGit func(string, ...string) (string, int), root, a, b string) (conflict bool, output string, failedCode int) {
	out, code := runGit(root, "merge-tree", "--write-tree", "--no-messages", a, b)
	switch code {
	case 0:
		return false, "", 0
	case 1:
		return true, "", 0
	default:
		return false, out, code
	}
}

// staleReason says why a branch behind the base must go back to its worker:
// "" when the merge is clean, so the gate merges it itself and verifies the
// merged tree. A conflict needs the worker's context. A shared file changed on
// both sides can merge textually clean and still break (one side widens a
// symbol, the other adds a call), but RE-VERIFY runs on the merged tree and
// catches that, so shared lists those files for the verdict note instead of
// refusing. Files matching round.sharedPaths are ignored, as the readiness
// overlap check ignores them. brokeMsg is set when a git check itself fails.
func (g *gateRun) staleReason() (why string, shared []string, brokeMsg string) {
	root := g.root
	landed, err := g.isAncestor(root, g.headRef, g.baseRef)
	if err != nil {
		return "", nil, err.Error()
	}
	if landed {
		return fmt.Sprintf("its work is already on %s, nothing to merge", g.o.Base), nil, ""
	}
	conflict, out, code := pairwiseMergeConflict(g.git, root, g.baseRef, g.headRef)
	if code != 0 {
		return "", nil, fmt.Sprintf("git merge-tree %s %s exited %d: %s", g.baseRef, g.headRef, code, out)
	}
	if conflict {
		return "the merge conflicts", nil, ""
	}
	mb, code := g.git(root, "merge-base", g.baseRef, g.headRef)
	if code != 0 || mb == "" {
		return "", nil, fmt.Sprintf("git merge-base %s %s exited %d", g.baseRef, g.headRef, code)
	}
	onBase, err1 := git.DiffFiles(g.ctx, g.e.Git, root, mb+".."+g.baseRef)
	onHead, err2 := git.DiffFiles(g.ctx, g.e.Git, root, mb+".."+g.headRef)
	if err1 != nil || err2 != nil {
		return "", nil, fmt.Sprintf("git diff --name-only against %s failed", mb)
	}
	return "", pairwisePaths(onBase, onHead, g.in.cfg.SharedPaths), ""
}
