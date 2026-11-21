package worker

import (
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/labels"
	"github.com/l4ci/rota/internal/land"
	"github.com/l4ci/rota/internal/landing"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/tracker"
	"slices"
	"strconv"
	"strings"
	"time"
)

// stepMerge is stage 3: the merge itself, through the forge or locally.
func (runner gateRun) stepMerge(result landing.GateResult, target GateTarget, forged gateForgeResult, remote bool, refs gateRefsResult, verified string) (string, landing.GateResult, bool, error) {
	g := runner.operation(result, target, forged)
	g.remote = remote
	g.headRef, g.baseRef, g.verified = refs.head, refs.base, refs.localHead
	g.verified = verified
	finish := func(done bool, err error) (string, landing.GateResult, bool, error) {
		return g.landed, *g.res, done, err
	}

	if tip := g.o.LandVerifiedTip; tip != "" && strings.TrimSpace(verified) != tip {
		g.verdict(landing.GateBaseMoved, fmt.Sprintf("BASE-MOVED %s — the PR head %s is not the tip %s its train verified; nothing landed", g.o.Slot, strutil.ShortSHA(verified), strutil.ShortSHA(tip)),
			"re-run the integration train, then gate the PR again")
		return finish(true, nil)
	}
	if want := g.o.LandVerifiedBase; want != "" {
		if base, _ := g.git(g.root, "rev-parse", g.baseRef); base != want {
			g.verdict(landing.GateBaseMoved, fmt.Sprintf("BASE-MOVED %s — %s moved from %s to %s since its train verified the tip; nothing landed", g.o.Slot, g.o.Base, strutil.ShortSHA(want), strutil.ShortSHA(base)),
				"gate the PR again: it verifies the tip on the new base")
			return finish(true, nil)
		}
	}
	if g.remote {
		if g.mergeRemote() {
			return finish(true, nil)
		}
	} else {
		var done bool
		g.landed, done = g.mergeLocal(g.root)
		if done {
			return finish(true, nil)
		}
	}
	g.res.Changed = true
	if g.remote {
		g.res.SHA, _ = g.git(g.root, "rev-parse", "--short=7", "HEAD")
	} else {
		g.res.SHA = strutil.ShortSHA(g.landed)
	}
	return finish(false, nil)
}

// mergeLocal merges the verified head into the base checked out in dir: the
// gate checkout, or a scratch tree. done is true when it ends the gate with a
// verdict.
func (g *gateRun) mergeLocal(dir string) (sha string, done bool) {
	run := g.e.Git.In(g.ctx, dir)
	base := g.o.Base
	if dir != g.root {
		base = "scratch tree"
	}
	sha, err := land.LandLocal(land.LocalPorts{Git: run, Recover: land.RecoveryGit(g.ctx, g.e.Git, dir)},
		land.LocalRequest{Head: g.verified, Base: base, Message: land.MergeSubject(g.branch, g.o.Base)})
	if err == nil {
		return sha, false
	}
	var post *land.PostMergeError
	if errors.As(err, &post) {
		if dir == g.root {
			g.res.Changed = true
			g.res.SHA = strutil.ShortSHA(sha)
		}
		g.broke(err.Error())
		return sha, true
	}
	msg, hint := land.Classify(err).Refusal(land.Words{
		Failed:   fmt.Sprintf("error: merge of %s into %s failed", g.branch, g.o.Base),
		Conflict: fmt.Sprintf("error: merge of %s into %s conflicted — resolve with the slot that owns the context", g.branch, g.o.Base),
	})
	g.verdict(landing.GateMergeFailed, msg, hint)
	return sha, true
}

// noteMergedRemotely marks the result when the forge says the PR is merged: a
// stale refusal of a PR landed elsewhere, by a squash or rebase too, whose head
// is then not on the base.
func (g *gateRun) noteMergedRemotely() {
	if !g.remote || g.o.CheckOnly { // a check-only gate writes no ledger entry
		return
	}
	if info, ok := g.prInfo(); ok && info.State == "MERGED" {
		g.res.AlreadyMerged = true
	}
}

// mergeRemote merges through the forge and confirms it landed. done is true
// when it ends the gate with a verdict.
func (g *gateRun) mergeRemote() (done bool) {
	// Pinned to the verified SHA so a push after the check is refused. The
	// adapter turns auto-merge off where the forge would otherwise schedule a
	// merge that reports success and merges nothing.
	n, _ := strconv.Atoi(g.prNum)
	if err := land.RequestForge(g.ctx, g.forge, n, g.verified, false); err != nil {
		g.verdict(landing.GateMergeFailed, fmt.Sprintf("error: %s merge failed for %s:\n%s", g.cliName, g.pr, tailLines(err.Error(), 20)), "")
		return true
	}
	sha, done := g.awaitMerged()
	if done {
		return true
	}
	g.landed = sha
	return g.confirmLanded(sha)
}

// awaitMerged waits for the forge to report the PR merged and returns its merge
// commit. The tracker's word is not enough, and the merge commit can take a
// moment to appear. State not merged is its own verdict (a scheduled auto-merge
// reports success and merges nothing). A fast-forward or rebase merge has no
// merge commit on either provider: state merged with an empty one falls back to
// the pinned verified SHA, which must then be on the base.
func (g *gateRun) awaitMerged() (sha string, done bool) {
	wait := 2 * time.Second
	if v := g.e.Getenv("ROTA_GATE_SHA_WAIT"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			wait = time.Duration(f * float64(time.Second))
		}
	}
	var state, body, title, head string
	for i := 0; i < 5; i++ {
		info, ok := g.prInfo()
		if !ok {
			g.broke(fmt.Sprintf("could not re-read PR %s after merging", g.prNum))
			return "", true
		}
		state, sha, body, title, head = info.State, info.MergeSHA, info.Body, info.Title, info.Head
		if state == "MERGED" && sha != "" {
			break
		}
		if state != "MERGED" {
			g.e.Sleep(wait)
		}
	}
	if state != "MERGED" {
		g.verdict(landing.GateNotMerged, fmt.Sprintf("NOT-MERGED %s — PR %s is %s after the merge call; nothing is on %s", g.o.Slot, g.prNum, state, g.o.Base), "")
		return "", true
	}
	src := tracker.ClosingSource{Body: body, Title: title, Branch: head}
	if g.closeMerged(src) {
		g.releaseClaimLabels(src)
	}
	if sha == "" {
		sha = g.verified
	}
	return sha, false
}

// confirmLanded checks the merge commit is on origin/<base> and fast-forwards
// the local base to it. Past this point the PR IS merged: local trouble is
// reported distinctly so nobody retries an already-merged PR.
func (g *gateRun) confirmLanded(sha string) (done bool) {
	o := g.o
	if _, code := g.git(g.root, "fetch", g.rem(), "-q"); code != 0 {
		g.broke("git fetch " + g.rem() + " failed after merging")
		return true
	}
	onBase, err := g.isAncestor(g.root, sha, g.baseRef)
	if err != nil {
		g.broke(err.Error())
		return true
	}
	if !onBase {
		g.verdict(landing.GateNotOnBase, fmt.Sprintf("NOT-ON-BASE %s — merge commit %s is not an ancestor of %s (merged into another branch?)", o.Slot, sha, g.baseRef), "")
		return true
	}
	g.res.Changed = true
	if len(sha) >= 7 {
		g.res.SHA = strutil.ShortSHA(sha) // the merge that landed on origin, for the merged-remotely verdicts
	}
	if out, code := g.git(g.root, "merge", "--ff-only", g.baseRef); code != 0 {
		g.mergedRemotely(fmt.Sprintf("local %s could not fast-forward (%s)", o.Base, g.ffFailureCause(out)))
		return true
	}
	inLocal, err := g.isAncestor(g.root, sha, "HEAD")
	if err != nil {
		g.mergedRemotely(fmt.Sprintf("could not check %s against the local %s (%v)", sha, o.Base, err))
		return true
	}
	if !inLocal {
		g.mergedRemotely(fmt.Sprintf("%s is not in the local %s", sha, o.Base))
		return true
	}
	return false
}

// mergedRemotely ends the gate on a PR that is on origin but not on the local
// base. The merged tree was never verified, and the result says so.
func (g *gateRun) mergedRemotely(why string) {
	g.res.VerifySkipped = true
	g.verdict(landing.GateMergedRemotely, fmt.Sprintf("MERGED-REMOTELY %s — PR %s is on %s but %s; do not re-merge, reconcile %s by hand", g.o.Slot, g.prNum, g.baseRef, why, g.o.Base),
		fmt.Sprintf("the merged tree was NOT verified: fix the local %s, fast-forward it to %s, then run the full gate on %s", g.o.Base, g.baseRef, g.o.Base))
}

// ffFailureCause names why `merge --ff-only` failed. Uncommitted changes to
// files the merge touches are told apart from real divergence: local main can
// be purely behind and still refuse to move.
func (g *gateRun) ffFailureCause(mergeOut string) string {
	incoming, _ := g.git(g.root, "diff", "--name-only", "HEAD", g.baseRef)
	in := map[string]bool{}
	for _, p := range strings.Split(incoming, "\n") {
		if p != "" {
			in[p] = true
		}
	}
	status, _ := g.git(g.root, "status", "--porcelain")
	var clash []string
	for _, l := range strings.Split(status, "\n") {
		if len(l) > 3 && in[l[3:]] {
			clash = append(clash, l[3:])
		}
	}
	if len(clash) > 0 {
		return "local changes would be overwritten: " + strings.Join(clash, ", ")
	}
	if counts, code := g.git(g.root, "rev-list", "--left-right", "--count", "HEAD..."+g.baseRef); code == 0 {
		if f := strings.Fields(counts); len(f) == 2 && f[0] != "0" {
			return fmt.Sprintf("diverged: %s ahead, %s behind %s", f[0], f[1], g.baseRef)
		}
	}
	if m := tailLines(mergeOut, 3); m != "" {
		return "merge refused: " + m
	}
	return "merge refused, cause not found: no overlapping local changes, not ahead of " + g.baseRef
}

// closeMerged closes linked milestone issues. On other bases, it closes the
// slot's issue and bundle on trackers implementing MergeCloser (Jira, Linear).
// A failed close is a warning because the PR has already landed. It returns
// whether every close succeeded, allowing the caller to sweep claim labels;
// milestone closes also release each successful issue's labels independently.
func (g *gateRun) closeMerged(src tracker.ClosingSource) bool {
	if id, ok := strings.CutPrefix(g.o.Base, "milestone/"); ok && id != "" && g.issues != nil {
		return g.closeOnMilestone(src, labels.OnMilestonePrefix+id)
	}
	mc, ok := g.issues.(tracker.MergeCloser)
	if !ok {
		return true
	}
	closed := true
	for _, key := range g.bundleKeys() { // the lead, then each member the PR names
		if !g.issues.ClosesIssue(key, src) {
			continue
		}
		is, err := g.issues.Get(g.ctx, key, false)
		if err == nil && slices.Contains(is.Labels, PartialSliceLabel) {
			continue
		}
		if err == nil {
			err = mc.CloseAfterMerge(g.ctx, key, g.pr)
		}
		if err != nil {
			g.res.Warnings = append(g.res.Warnings, fmt.Sprintf("ISSUE-OPEN %s — %s merged but %s could not be closed, so it keeps its claim for `rota round reconcile`: %v", g.o.Slot, g.pr, key.Ref(), err))
			closed = false
		}
	}
	return closed
}
