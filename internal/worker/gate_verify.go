package worker

import (
	"fmt"
	"github.com/l4ci/rota/internal/change"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/landing"
	"github.com/l4ci/rota/internal/proof"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/tracker"
	"strconv"
	"strings"
	"time"
)

// stepOnBase refuses a gate run with another branch than the base checked out.
func (runner gateRun) stepOnBase(result landing.GateResult) (struct{}, landing.GateResult, bool, error) {
	g := runner.operation(result, GateTarget{}, gateForgeResult{})
	finish := func(done bool, err error) (struct{}, landing.GateResult, bool, error) {
		return struct{}{}, *g.res, done, err
	}

	cur, _ := g.git(g.root, "rev-parse", "--abbrev-ref", "HEAD")
	if cur != g.o.Base {
		return finish(true, fail(exitcode.ExitResolution, fmt.Sprintf("gate must run with %s checked out (currently on %s)", g.o.Base, cur)))
	}
	return finish(false, nil)
}

// stepCIVerifier builds the CI verifier of a test.fullWhere ci gate.
func (runner gateRun) stepCIVerifier(result landing.GateResult, forged gateForgeResult) (*ciVerifier, landing.GateResult, bool, error) {
	g := runner.operation(result, GateTarget{}, forged)
	finish := func(done bool, err error) (*ciVerifier, landing.GateResult, bool, error) {
		return g.ci, *g.res, done, err
	}

	if g.in.cfg.where != WhereCI || g.o.skipsVerify() {
		return finish(false, nil)
	}
	ci, msg := g.e.newCIVerifier(g.ctx, g.root, g.forge, g.in.cfg)
	if msg != "" {
		return finish(g.broke(msg))
	}
	g.ci = ci
	return finish(false, nil)
}

// stepPremerge runs the pre-merge sequence the train shares (premerge.go):
// ledger expiry, no-verify, CI-config and the merge-approval gate (B1).
func (runner gateRun) stepPremerge(result landing.GateResult, target GateTarget, refs gateRefsResult, verified string, ci *ciVerifier) (struct{}, landing.GateResult, bool, error) {
	g := runner.operation(result, target, gateForgeResult{})
	g.headRef, g.baseRef, g.verified = refs.head, refs.base, refs.localHead
	g.verified = verified
	g.ci = ci
	finish := func(done bool, err error) (struct{}, landing.GateResult, bool, error) {
		return struct{}{}, *g.res, done, err
	}

	run := premergeRun{
		Subject:      gateSubject(g.o.Slot),
		In:           g.in,
		Now:          g.e.Now(),
		SkipLedger:   g.o.CheckOnly || g.o.skipsVerify(),
		SkipNoVerify: g.o.skipsVerify(),
		Files:        g.changedFiles,
	}
	if g.ci != nil {
		run.CIChanged = func() ([]string, error) {
			return git.DiffFiles(g.ctx, g.e.Git, g.root, g.baseRef+"..."+g.verified)
		}
	}
	if g.o.Approval != nil {
		run.Approve = func(files func() ([]string, error)) (*exitcode.Refusal, error) {
			return g.o.Approval.Check(g.o.Slot+" into "+g.o.Base, files, func() (landing.ApprovalThread, error) {
				return TargetApprovalThread(g.target)
			})
		}
	}
	r, approval, ref, err := run.run()
	switch {
	case approval:
		g.res.Verdict = landing.GateApprovalRequired
		g.res.Refusal = ref
		if ref != nil {
			err = ref.Err
		}
		return finish(true, err)
	case err != nil:
		return finish(g.broke(err.Error()))
	case r != nil:
		g.res.Expired = r.Expired
		g.verdict(r.Verdict, r.Err, r.Hint)
		return finish(true, nil)
	}
	return finish(false, nil)
}

// stepForgeMergeable asks the forge about mergeability before the scratch verify.
func (runner gateRun) stepForgeMergeable(result landing.GateResult, target GateTarget, forged gateForgeResult, remote bool) (struct{}, landing.GateResult, bool, error) {
	g := runner.operation(result, target, forged)
	g.remote = remote
	finish := func(done bool, err error) (struct{}, landing.GateResult, bool, error) {
		return struct{}{}, *g.res, done, err
	}

	if g.remote && !g.o.skipsVerify() {
		return finish(g.forgeRefuses(), nil)
	}
	return finish(false, nil)
}

// stepTier puts the change in a tier (#2348) and picks what verifies the
// scratch merge: test.docs or test.small in place of test.full and test.e2e
// when that list is set. It runs after stepPremerge, so the NO-VERIFY refusal
// still reads test.full, and it fails closed: anything ClassifyChange cannot
// read is full. Under test.fullWhere ci the tier is recorded, not applied.
func (runner gateRun) stepTier(result landing.GateResult, target GateTarget, forged gateForgeResult, refs gateRefsResult, verified string, ci *ciVerifier) (*GateConfig, landing.GateResult, bool, error) {
	g := runner.operation(result, target, forged)
	g.headRef, g.baseRef, g.verified = refs.head, refs.base, refs.localHead
	g.verified = verified
	g.ci = ci
	finish := func(done bool, err error) (*GateConfig, landing.GateResult, bool, error) {
		return g.tierCfg, *g.res, done, err
	}

	if g.o.skipsVerify() {
		return finish(false, nil)
	}
	span := g.baseRef + "..." + g.verified
	tier := g.classify(span)
	g.res.Tier, g.res.TierReason = string(tier.Tier), tier.Reason
	if g.ci != nil {
		if tier.Tier != change.TierFull {
			g.res.Notes = append(g.res.Notes, fmt.Sprintf("GATE-TIER %s — %s (%s); not applied: test.fullWhere is ci, so CI runs its own checks", g.o.Slot, tier.Tier, tier.Reason))
		}
		return finish(false, nil)
	}
	var files []string
	if tier.Tier == change.TierSmall && proof.HasFiles(g.in.cfg.Small) {
		var err error
		// Deleted files stay in: a removed file's package and its dependents must run.
		if files, err = git.DiffFiles(g.ctx, g.e.Git, g.root, span); err != nil {
			g.res.Tier, g.res.TierReason = string(change.TierFull), fmt.Sprintf("could not list the files of %s for test.small: %v", span, err)
			return finish(false, nil)
		}
	}
	cfg, ran, skipped := g.in.cfg.forTier(tier.Tier, files)
	if len(skipped) == 0 {
		return finish(false, nil)
	}
	g.tierCfg, g.res.Skipped = &cfg, skipped
	g.res.Notes = append(g.res.Notes, fmt.Sprintf("GATE-TIER %s — %s (%s); ran %s; skipped %s", g.o.Slot, tier.Tier, tier.Reason, ran, strings.Join(skipped, ", ")))
	return finish(false, nil)
}

// classify is the change tier of span, with the PR's Door line when there is a PR.
func (g *gateRun) classify(span string) change.Result {
	policy, perr := g.in.cfg.ChangePolicy()
	src := ChangeSources{Policy: policy, PolicyErr: perr, Git: g.e.Git, Dir: g.root, Span: span}
	if g.pr != "" {
		src.PRBody = g.prBody
	}
	return ClassifyChange(g.ctx, src)
}

// stepVerifyFirst verifies the merge result in a scratch tree, before anything
// lands (stage 2).
func (runner gateRun) stepVerifyFirst(result landing.GateResult, target GateTarget, forged gateForgeResult, remote bool, refs gateRefsResult, verified string, ci *ciVerifier, tierCfg *GateConfig) (gateVerification, landing.GateResult, bool, error) {
	g := runner.operation(result, target, forged)
	g.remote = remote
	g.headRef, g.baseRef, g.verified = refs.head, refs.base, refs.localHead
	g.verified = verified
	g.ci = ci
	g.tierCfg = tierCfg
	finish := func(done bool, err error) (gateVerification, landing.GateResult, bool, error) {
		return gateVerification{tree: g.verifiedTree, ciRun: g.ciRun}, *g.res, done, err
	}

	if g.o.skipsVerify() {
		return finish(false, nil)
	}
	check, what := g.verifyLocal, "the gate"
	if g.ci != nil {
		ci := g.ci
		check, what = func(dir, sha string) (bool, error) { return g.verifyOnCI(ci, sha) }, "CI"
	}
	done, err := g.verifyFirst(what, check)
	return finish(done || err != nil, err)
}

// gateMergeableTries bounds the wait for a forge still computing mergeability.
const gateMergeableTries = 3

// forgeRefuses asks the forge whether the PR merges cleanly before the scratch
// verify, so a conflict only the forge sees (local git follows renames the
// forge does not) costs seconds, not a verify run. done is true when the forge
// reports a conflict: merge-failed, nothing landed. Unknown after a few tries
// and any error proceed: the post-verify forge merge stays the authority.
func (g *gateRun) forgeRefuses() (done bool) {
	n, err := strconv.Atoi(g.prNum)
	if err != nil {
		return false
	}
	for i := 0; i < gateMergeableTries; i++ {
		m, err := g.forge.PRMergeable(g.ctx, n)
		if err != nil {
			return false
		}
		switch m.State {
		case tracker.MergeConflict:
			why := m.Reason
			if why == "" {
				why = "the merge commit cannot be cleanly created"
			}
			g.verdict(landing.GateMergeFailed, fmt.Sprintf("error: %s merge failed for %s:\n%s reports it is not mergeable (%s); nothing was verified", g.cliName, g.pr, g.provider, why), "")
			return true
		case tracker.MergeUnknown:
			if i < gateMergeableTries-1 {
				g.e.Sleep(time.Second)
			}
		default:
			return false
		}
	}
	return false
}

// verifyFirst is RE-VERIFY moved before the merge (#494; test.fullWhere ci
// did it first): the merge result is built in a scratch tree and check
// verifies it there, so a red result bounces the slot with nothing landed
// where verifying the landed base would leave it to fix forward. The base must
// still be what the scratch tree was built on, and its tree is what must land
// (stepVerify). done is true when it ends the gate with a verdict.
func (g *gateRun) verifyFirst(what string, check func(dir, sha string) (bool, error)) (bool, error) {
	o := g.o
	baseSHA, code := g.git(g.root, "rev-parse", g.baseRef)
	if code != 0 {
		return g.broke(fmt.Sprintf("git rev-parse %s exited %d", g.baseRef, code))
	}
	// The train's scratch shape: a gate is a train of one. The CI verify keeps
	// its own prefix; the worktree guard ignores both.
	prefix := "rota-train-"
	if g.in.cfg.where == WhereCI {
		prefix = "rota-ci-"
	}
	dir, cleanup, err := g.e.scratchTree(g.ctx, g.root, baseSHA, prefix)
	if err != nil {
		return g.broke(err.Error())
	}
	defer cleanup()
	sha, done := g.mergeLocal(dir)
	if done {
		return true, nil
	}
	tree, _ := g.git(dir, "rev-parse", "HEAD^{tree}")
	g.res.SHA = strutil.ShortSHA(sha)
	g.res.MergeCommit = g.res.SHA
	if done, err := check(dir, sha); done || err != nil {
		return true, err
	}
	if done, err := g.vacuousRun(baseSHA, sha); done || err != nil {
		return true, err
	}
	if done, err := g.behaviourRun(dir); done || err != nil {
		return true, err
	}
	if g.remote {
		if _, code := g.git(g.root, "fetch", g.rem(), "-q"); code != 0 {
			return g.broke("git fetch " + g.rem() + " failed after " + what + " verified")
		}
	}
	if cur, _ := g.git(g.root, "rev-parse", g.baseRef); cur != baseSHA {
		g.verdict(landing.GateBaseMoved, fmt.Sprintf("BASE-MOVED %s — %s moved from %s to %s while %s verified; nothing landed", o.Slot, g.baseRef, strutil.ShortSHA(baseSHA), strutil.ShortSHA(cur), what),
			"re-run the gate on the new base")
		return true, nil
	}
	g.verifiedTree = tree
	return false, nil
}

// verifyOnCI pushes the scratch merge sha for CI to check (test.fullWhere ci).
func (g *gateRun) verifyOnCI(ci *ciVerifier, sha string) (bool, error) {
	o := g.o
	vr, err := ci.verify(sha, o.Slot)
	if err != nil {
		return g.broke(err.Error())
	}
	w := g.tierWalk(nil, func(string, VerifyResult) (bool, error) {
		g.verdict(landing.GateVerifyFailed, fmt.Sprintf("GATE-FAIL %s — the merge result of %s does not pass CI; nothing landed\n%s", o.Slot, g.branch, vr.detail()),
			fmt.Sprintf("send %s back to fix it, then re-gate", o.Slot))
		return true, nil
	})
	if ended, _, err := w.take(tierFull, vr); ended || err != nil {
		return true, err
	}
	g.ciRun = &vr
	return false, nil
}

// verifyLocal runs test.full, then test.e2e, on the scratch merge in dir.
func (g *gateRun) verifyLocal(dir, _ string) (bool, error) {
	cfg := g.in.cfg
	if g.tierCfg != nil {
		cfg = *g.tierCfg
	}
	return g.verifyTiers(cfg, tierFull, dir, false)
}

// verifyTiers walks the tiers from first in dir through the shared walk,
// ending at the first that fails. landed says the tree is already on the base:
// a red result is then fixed forward, else nothing landed and the slot goes
// back. An empty test.full leaves a NO-VERIFY note; test.e2e passing after it
// clears VerifySkipped. done is true when it ends the gate with a verdict.
func (g *gateRun) verifyTiers(cfg GateConfig, first, dir string, landed bool) (bool, error) {
	w := g.tierWalk(func(tier string, cmds []string) (VerifyResult, error) {
		run := g.e.runGateVerify
		if tier == tierE2E {
			run = g.e.runE2E
		}
		vr, err := run(g.ctx, cmds, dir, g.in.ledger)
		if err == nil {
			prs := []string{}
			if g.res.PR != "" {
				prs = append(prs, g.res.PR)
			}
			measuredTier := tier
			if tier == tierFull && g.tierCfg != nil {
				measuredTier = "test." + g.res.Tier
			}
			g.e.recordVerification(g.root, g.o.Round, measuredTier, GateTarget{Name: g.res.TargetSlot, Issue: g.res.Issue, PR: g.res.PR}, prs, vr)
			err = g.e.recordFlakes(g.root, g.o.Round, prs, vr)
		}
		return vr, err
	}, g.redTier(landed))
	ended, err := w.walk(cfg, first)
	if w.fullEmpty {
		g.res.VerifySkipped = true
	}
	if w.e2eGreen {
		g.res.VerifySkipped = false
	}
	return ended, err
}

// tierWalk builds the gate's walk: it records into the result and stops on a
// run that gave no answer (only a CI run can). Only run and red differ.
func (g *gateRun) tierWalk(run func(string, []string) (VerifyResult, error), red func(string, VerifyResult) (bool, error)) *tierWalk {
	res := g.res
	return &tierWalk{
		subject: g.o.Slot,
		run:     run,
		stop: func(vr VerifyResult) bool {
			v, msg, hint := vr.stopVerdict(g.o.Slot)
			if v == "" {
				return false
			}
			g.verdict(v, msg, hint)
			return true
		},
		red:   red,
		notes: &res.Notes, verified: &res.Verified, excluded: &res.Excluded, flaky: &res.Flaky,
	}
}

// landedTree is the tree of the commit the merge produced.
func (g *gateRun) landedTree() string {
	tree, _ := g.git(g.root, "rev-parse", g.landed+"^{tree}")
	return tree
}

// confirmCITree checks the tree that landed is the one CI verified, or the
// forge merged something else (the base moved in between). It reads the merge
// commit itself, not the base's tip: a push to the base after the merge is not
// a mismatch. false ends the gate with the verdict set.
func (g *gateRun) confirmCITree() bool {
	if tree := g.landedTree(); tree != g.verifiedTree {
		g.verdict(landing.GateBaseMoved, fmt.Sprintf("BASE-MOVED %s — it landed, but the landed tree %s differs from the tree CI verified (%s); %s changed during the merge", g.o.Slot, strutil.ShortSHA(tree), strutil.ShortSHA(g.verifiedTree), g.o.Base),
			fmt.Sprintf("do not re-merge; verify %s as it is now", g.o.Base))
		return false
	}
	return true
}

// changedFiles lists the paths the merge changes, including both names of a
// rename, for the approval gate (the same rule as the train footprint).
func (g *gateRun) changedFiles() ([]string, error) {
	return git.DiffFiles(g.ctx, g.e.Git, g.root, g.baseRef+"..."+g.verified)
}

// stepVerify is stage 3. The merge result was verified before the merge
// (verifyFirst); what landed must be that tree. A local verify whose landed
// tree differs (the base moved between the last check and the forge merge) is
// run again on the landed base, so the gate never claims a verify it did not
// do. Under test.fullWhere ci a mismatch is base-moved, and test.e2e runs here
// on the landed tree.
func (runner gateRun) stepVerify(result landing.GateResult, target GateTarget, forged gateForgeResult, remote bool, refs gateRefsResult, verified string, verification gateVerification, landed string) (struct{}, landing.GateResult, bool, error) {
	g := runner.operation(result, target, forged)
	g.remote = remote
	g.headRef, g.baseRef, g.verified = refs.head, refs.base, refs.localHead
	g.verified = verified
	g.verifiedTree, g.ciRun = verification.tree, verification.ciRun
	g.landed = landed
	finish := func(done bool, err error) (struct{}, landing.GateResult, bool, error) {
		return struct{}{}, *g.res, done, err
	}

	res, o := g.res, g.o
	if o.skipsVerify() {
		res.Verdict, res.VerifySkipped = landing.GatePass, true
		if !o.LandVerified {
			res.Notes = append(res.Notes, noVerifyFlagNote(o.Slot))
		}
		return finish(true, nil)
	}
	if g.ciRun != nil {
		if !g.confirmCITree() {
			return finish(true, nil)
		}
		if done, err := g.verifyTiers(g.in.cfg, tierE2E, g.root, true); done || err != nil {
			return finish(true, err)
		}
		res.Verdict = landing.GatePass
		return finish(true, nil)
	}
	if tree := g.landedTree(); tree != g.verifiedTree {
		res.Notes = append(res.Notes, fmt.Sprintf("VERIFY-AGAIN %s — the landed tree %s differs from the verified scratch merge %s (%s moved before the merge); verifying the landed %s", o.Slot, strutil.ShortSHA(tree), strutil.ShortSHA(g.verifiedTree), o.Base, o.Base))
		res.Verified, res.Excluded, res.VerifySkipped = nil, nil, false
		// The landed base holds other merges, so the cheap tier's premise (only
		// this change's files changed) no longer holds: verify it in full.
		if done, err := g.verifyTiers(g.in.cfg, tierFull, g.root, true); done || err != nil {
			return finish(true, err)
		}
	}
	res.Verdict = landing.GatePass
	return finish(true, nil)
}

// redTier is the gate's answer to a failed tier. landed says the tree is
// already on the base: a red result is then fixed forward, else nothing landed
// and the slot goes back. It always ends the gate with a verdict.
func (g *gateRun) redTier(landed bool) func(string, VerifyResult) (bool, error) {
	return func(tier string, vr VerifyResult) (bool, error) {
		res, o := g.res, g.o
		e2e := tier == tierE2E
		what := "verification"
		if e2e {
			what = "test.e2e"
		}
		detail := vr.detail()
		if e2e {
			detail = fmt.Sprintf("last lines of the e2e output (full log: %s):\n%s", vr.LogPath, indentTail(vr.Log, 20))
		}
		if landed {
			if err := g.freezeFailure(vr); err != nil {
				return true, err
			}
			g.verdict(landing.GateVerifyFailed, fmt.Sprintf("GATE-FAIL %s — merged tree does not pass %s at %s\n%s", o.Slot, what, res.SHA, detail),
				fmt.Sprintf("fix forward on %s; the owning slot has usually moved on", o.Base))
			return true, nil
		}
		g.verdict(landing.GateVerifyFailed, fmt.Sprintf("GATE-FAIL %s — the merge of %s into %s does not pass %s at %s; nothing landed\n%s", o.Slot, g.branch, o.Base, what, res.SHA, detail),
			fmt.Sprintf("send %s back to fix it, then re-gate", o.Slot))
		return true, nil
	}
}
