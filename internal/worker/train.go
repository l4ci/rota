package worker

import (
	"context"
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/land"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/testledger"
	"os"
	"sort"
	"strings"
)

// Merge train (#83): verify several queued PRs together once, then land them.
//
// One serial gate is the slowest step of a round (#46), and N PRs gated one at a
// time pay it N times. A train pays it once. In order:
//
//  1. CHECK each target the way `gate --check-only` does (freshness, PR
//     identity, provenance, review verdict). Any refusal stops the train, naming that target.
//  2. APPROVE once for the whole train (B1 merge approval over the union of the
//     files the members change), before the expensive step.
//  3. MERGE the members in order onto the base in a scratch worktree. A conflict
//     names the member that does not fit on the base plus the members before it.
//  4. VERIFY the scratch tree once with test.full, then once with test.e2e (#378).
//  5. On a pass LAND every member through the ordinary gate (forge merge, pinned
//     to the verified head), in order, without re-verifying. Before the first
//     landing the base and every head must still be what the scratch tree was
//     built from; otherwise nothing lands.
//  6. On a fail of either tier BISECT: verify growing prefixes of the train to find the first
//     member whose addition breaks the tree and name it. That is the member to
//     send back; it can be an interaction with the members before it, not only
//     the member alone. With LandGreen the verified prefix before it lands.

// GateBaseMoved: the base or a member's head moved while the train verified, so
// the verified tree is no longer what would land. Nothing landed.
const GateBaseMoved = "base-moved"

// TrainOpts are the flags of `rota worker train`.
type TrainOpts struct {
	Targets []string // slot names or PR refs, in merge order
	// Round reads the round lease for the train's ledger entries; Train makes
	// one when nil, so the whole train reads the lease once.
	Round *RoundMemo
	Base  string
	// LandGreen lands the verified prefix before the culprit when the train
	// fails verification.
	LandGreen bool
	// NoVerify lets the train run with an empty test.full (it is refused
	// otherwise); a set test.full is still run.
	NoVerify bool
	// Approve is GateOpts.Approve for the whole train: files lists the union of
	// the paths the members change.
	Approve func(files func() ([]string, error)) error
	// Verdict is GateOpts.Verdict, run for every member's check.
	Verdict func(branch string) error
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
	Base        string
	Verdict     string
	Members     []TrainMember
	Culprit     string
	Verified    []string // test.full commands that passed
	E2EVerified []string // test.e2e commands that passed
	Landed      []string
	Changed     bool
	SHA         string
	Err         string
	Hint        string
	Notes       []string
	// CacheHits names each verify answered from the verdict cache instead of
	// being run; Transient the members earlier named culprit that this train
	// passed with (#400).
	CacheHits []string
	Transient []string
	// Excluded lists the test-ledger entries that excused a failing command;
	// Expired the entries past their expiry, which fail the train.
	Excluded, Expired []testledger.Entry
}

// OK reports a train that verified and landed whole.
func (r TrainResult) OK() bool { return r.Verdict == GatePass }

// Train runs a merge train (see above). The base must be checked out in root.
func (e Env) Train(ctx context.Context, root string, o TrainOpts) (TrainResult, error) {
	e = e.withDefaults()
	if o.Round == nil {
		o.Round = &RoundMemo{}
	}
	var res TrainResult
	err := e.withLandLock(ctx, root, func() (err error) {
		cache := loadTrainCache(root)
		gated := map[string]bool{}
		res, err = e.train(ctx, root, o, cache, gated)
		trainLedger(o.Round, root, res, gated)
		if cache.dirty {
			if serr := cache.save(); serr != nil {
				res.Notes = append(res.Notes, "TRAIN-CACHE not saved — "+serr.Error())
			}
		}
		return err
	})
	return res, err
}

// gated collects the members the landing loop ran a real gate on; each of those
// wrote its own ledger entry.
func (e Env) train(ctx context.Context, root string, o TrainOpts, cache *trainCache, gated map[string]bool) (TrainResult, error) {
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
	// A bad test.fullWhere or test.ciChecks is refused before any member is
	// checked; fullTier reads them again once the members are known.
	if _, err := FullWhere(config.Load(rotatree.Config(root))); err != nil {
		return res, err
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
		gr, err := e.Gate(ctx, root, GateOpts{Slot: t, Base: o.Base, CheckOnly: true, Verdict: o.Verdict})
		if err != nil {
			if gr.Verdict == GateVerdictBlocked {
				res.Verdict, res.Culprit = gr.Verdict, t
			}
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
	// An empty test.full would land the train unverified: refuse before the
	// scratch merge unless --no-verify says so.
	if where, _ := FullWhere(config.Load(rotatree.Config(root))); !o.NoVerify && noVerifyRule(where, verifyCommandsAt(root), TierCommands(root, "e2e")) {
		res.Verdict = GateNoVerify
		res.Err, res.Hint = noVerifyRefusal("TRAIN")
		return res, nil
	}
	// The ledger is read before anything merges, like the config. An expired
	// entry fails the train before it builds the scratch tree.
	led, err := LoadLedger(root)
	if err != nil {
		return res, err
	}
	if now := e.Now(); LedgerExpiry(led, now) != "" {
		res.Verdict, res.Expired = GateVerifyFailed, led.Expired(now)
		res.Err = "TRAIN-FAIL " + LedgerExpiry(led, now) + "; nothing landed"
		res.Hint = "fix the test or renew the entry in .rota/test-ledger.json, then re-run the train"
		return res, nil
	}
	verify, onCI, brokeMsg, err := e.fullTier(ctx, root, "train")
	if err != nil {
		return res, err
	}
	if brokeMsg != "" {
		return e.trainBroke(res, brokeMsg)
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

	// The union of the files the members change.
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
	if onCI {
		var changed []string
		for _, m := range res.Members {
			f, err := e.gateEnv().ciDiffFiles(root, baseRef, headRef(m))
			if err != nil {
				return e.trainBroke(res, err.Error())
			}
			changed = append(changed, f...)
		}
		if msg, hint := ciConfigRefusal("train", changed); msg != "" {
			res.Verdict, res.Err, res.Hint = GateCIConfigChanged, msg, hint
			return res, nil
		}
	}

	// 2. One approval for the train.
	if o.Approve != nil {
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
	cache.retainBase(baseSHA)
	ge := e.gateEnv()
	ge.ctx = ctx // cleanup still runs on a context cut loose from it
	scratch, cleanup, err := ge.scratchTree(root, baseSHA, "rota-train-")
	if err != nil {
		return e.trainBroke(res, err.Error())
	}
	defer cleanup()
	tips := make([]string, len(res.Members)+1) // tips[i]: the scratch tree with the first i members merged
	tips[0] = baseSHA
	for i, m := range res.Members {
		run := func(args ...string) (git.Result, error) { return e.Git(ctx, scratch, args...) }
		if merr := land.MergeLocal(run, heads[i], fmt.Sprintf("train: %s into %s", m.Branch, o.Base), land.RecoveryGit(ctx, e.Git, scratch)); merr != nil {
			res.Culprit = m.Target
			res.Members[i].Culprit = true
			res.Verdict = GateMergeFailed
			var me *land.MergeError
			switch {
			case errors.As(merr, new(*land.CleanupError)):
				res.Err = fmt.Sprintf("TRAIN-FAIL %s — merging %s into the scratch tree failed: %s", m.Target, m.Branch, merr)
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

	// 4. Verify once: test.full, then test.e2e on the same tree.
	n := len(res.Members)
	passing := n
	// A local run that cannot start is an error, as before; a CI push or
	// forge read that fails is CHECK-BROKE, since nothing has landed.
	verifyErr := func(err error) (TrainResult, error) {
		if onCI {
			return e.trainBroke(res, err.Error())
		}
		return res, err
	}
	// cached answers a verify of the base plus its first k members from the
	// cache when the same tier ran on the same base and heads before.
	cached := func(tier string, run func() (VerifyResult, error)) func(int) (VerifyResult, error) {
		return func(k int) (VerifyResult, error) {
			key := trainKey(tier, baseSHA, heads[:k])
			if v, ok := cache.get(key); ok {
				res.CacheHits = append(res.CacheHits, fmt.Sprintf("%s: base + first %d member(s)", tier, k))
				return VerifyResult{Cached: true, Verified: v.Verified}, nil
			}
			r, err := run()
			if err == nil {
				cache.put(key, r)
			}
			return r, err
		}
	}
	// blame remembers the member the bisect named, to recognise it as flaky later.
	blame := func(tier string) {
		for i, m := range res.Members {
			if m.Culprit && res.Verdict == GateVerifyFailed {
				cache.blame(m.Branch, heads[i], trainKey(tier, baseSHA, heads[:i+1]))
			}
		}
	}
	fullTierName := "test.full"
	if onCI {
		fullTierName = "test.full@ci"
	}
	full := cached(fullTierName, func() (VerifyResult, error) { return verify(scratch) })
	vr, err := full(n)
	if err != nil {
		return verifyErr(err)
	}
	if r, done := trainStopped(res, vr); done {
		return r, nil
	}
	green := true // test.full passed (or had nothing to run), so test.e2e may run
	if vr.NoCommands {
		res.Notes = append(res.Notes, "NO-VERIFY train — test.full is empty; the merged tree was NOT gated by a command.",
			"set test.full via rota config set to make this gate real")
	} else {
		res.Verified = vr.Verified
		res.Excluded = append(res.Excluded, vr.Excluded...)
		for _, c := range vr.Failed {
			res.Notes = append(res.Notes, "verify FAILED: "+c)
		}
		if !vr.OK() {
			green = false
			var stop bool
			passing, stop, err = e.bisectTrain(scratch, &res, tips, "test.full", full, vr, o.LandGreen)
			blame(fullTierName)
			if err != nil {
				return verifyErr(err)
			} else if stop {
				return res, nil
			}
		}
	}

	// 4b. E2E: the most expensive tier runs once, on the train result, after
	// test.full passed. A red e2e bisects the same way a red full does.
	e2e := TierCommands(root, "e2e")
	if green && len(e2e) > 0 {
		run := cached("test.e2e", func() (VerifyResult, error) { return e.RunVerifyWith(ctx, e2e, scratch, led) })
		er, err := run(n)
		if err != nil {
			return res, err
		}
		res.E2EVerified = er.Verified
		res.Excluded = append(res.Excluded, er.Excluded...)
		for _, c := range er.Failed {
			res.Notes = append(res.Notes, "e2e FAILED: "+c)
		}
		if !er.OK() {
			var stop bool
			passing, stop, err = e.bisectTrain(scratch, &res, tips, "test.e2e", run, er, o.LandGreen)
			blame("test.e2e")
			if err != nil || stop {
				return res, err
			}
		}
	}

	// A member earlier named culprit that this verified train carries was flaky:
	// the failure did not reproduce on a different combination.
	if passing == n && !vr.NoCommands {
		for i, m := range res.Members {
			if cache.forgive(m.Branch, heads[i]) {
				res.Transient = append(res.Transient, m.Target)
				res.Notes = append(res.Notes, fmt.Sprintf("TRANSIENT %s — named culprit by an earlier train, but this train with it passes verification; treated as flaky, not blamed again", m.Target))
			}
		}
	}

	if !green && passing > 0 && len(e2e) > 0 {
		res.Notes = append(res.Notes, fmt.Sprintf("NO-E2E land-green — the verified first %d member(s) land without test.e2e; it never ran on that prefix.", passing))
	}

	// 5. Land. The tree that was verified must still be the one that lands.
	if remote {
		if _, code := e.git(root, "fetch", "origin", "-q"); code != 0 {
			return e.trainBroke(res, "git fetch origin failed before landing")
		}
	}
	verdict, culprit := res.Verdict, res.Culprit
	if cur, _ := e.git(root, "rev-parse", baseRef); cur != baseSHA {
		return e.trainMoved(res, "", fmt.Sprintf("%s moved from %s to %s while the train verified", baseRef, strutil.ShortSHA(baseSHA), strutil.ShortSHA(cur)))
	}
	for i := 0; i < passing; i++ {
		if cur, _ := e.git(root, "rev-parse", headRef(res.Members[i])); cur != heads[i] {
			return e.trainMoved(res, res.Members[i].Target, fmt.Sprintf("%s moved from %s to %s while the train verified", headRef(res.Members[i]), strutil.ShortSHA(heads[i]), strutil.ShortSHA(cur)))
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
				return e.trainMoved(res, m.Target, fmt.Sprintf("%s changed outside the train after %d landing(s) (tree %s, verified %s)", baseRef, i, strutil.ShortSHA(got), strutil.ShortSHA(want)))
			}
		}
		gated[m.Target] = true
		gr, err := e.Gate(ctx, root, GateOpts{Slot: m.Target, Base: o.Base, NoVerify: true, Train: true, Round: o.Round})
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
			return e.trainMoved(res, "", fmt.Sprintf("the landed tree %s differs from the verified scratch tree %s; the forge merged differently than git did", strutil.ShortSHA(tree), strutil.ShortSHA(want)))
		}
	}
	res.Verdict, res.Culprit = verdict, culprit
	if verdict == "" {
		res.Verdict = GatePass
	}
	return res, nil
}

func (e Env) trainBroke(res TrainResult, msg string) (TrainResult, error) {
	res.Verdict, res.Err = GateCheckBroke, "CHECK-BROKE train — "+msg
	return res, nil
}

// trainStopped ends the train when a CI run gave no answer (ci-not-run,
// verify-timeout). Nothing has landed at any point it is called.
func trainStopped(res TrainResult, vr VerifyResult) (TrainResult, bool) {
	v, msg, hint := vr.stopVerdict("train")
	if v == "" {
		return res, false
	}
	res.Verdict, res.Err, res.Hint = v, msg, hint
	return res, true
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

// bisectTrain finds the first member whose merge makes tier fail, using verify
// on growing prefixes of the train (tips), and writes the verdict into res.
// failed is the failed full-train run; the caller hands its log over. It
// assumes the base is green: a red base is reported as such. passing is how
// many leading members may still land (all but the culprit under LandGreen);
// stop is true when the train must end here with res as the answer.
func (e Env) bisectTrain(scratch string, res *TrainResult, tips []string, tier string, verify func(int) (VerifyResult, error), failed VerifyResult, landGreen bool) (passing int, stop bool, err error) {
	broke := func(msg string) (int, bool, error) {
		*res, _ = e.trainBroke(*res, msg)
		return 0, true, nil
	}
	// run verifies one prefix; done means a CI run gave no answer and res holds it.
	run := func(k int) (VerifyResult, bool, error) {
		r, err := verify(k)
		if err != nil {
			return r, true, err
		}
		if r2, done := trainStopped(*res, r); done {
			os.Remove(failed.LogPath)
			*res = r2
			return r, true, nil
		}
		return r, false, nil
	}
	lo, hi := 0, len(res.Members)
	for hi-lo > 1 {
		mid := (lo + hi) / 2
		if _, code := e.git(scratch, "checkout", "-q", "--detach", tips[mid]); code != 0 {
			return broke("git checkout " + tips[mid] + " failed in the scratch tree")
		}
		pr, done, err := run(mid)
		if done {
			return 0, true, err
		}
		if pr.OK() {
			lo = mid
		} else {
			os.Remove(failed.LogPath)
			failed, hi = pr, mid
		}
	}
	if lo == 0 { // bisect assumes a green base; the first member only looks guilty on a red one
		if _, code := e.git(scratch, "checkout", "-q", "--detach", tips[0]); code != 0 {
			return broke("git checkout " + tips[0] + " failed in the scratch tree")
		}
		br, done, err := run(0)
		if done {
			return 0, true, err
		}
		if !br.OK() {
			os.Remove(failed.LogPath)
			res.Verdict = GateVerifyFailed
			res.Err = fmt.Sprintf("TRAIN-FAIL base — the base fails verification on its own (%s), so no member can be blamed\n%s",
				tier, br.detail())
			res.Hint = "fix the base, then re-run the train"
			return 0, true, nil
		}
	}
	c := res.Members[hi-1]
	res.Members[hi-1].Culprit = true
	res.Culprit, res.Verdict = c.Target, GateVerifyFailed
	res.Err = fmt.Sprintf("TRAIN-FAIL %s — the train fails %s once %s (%s) is merged; the first %d member(s) pass\n%s",
		c.Target, tier, c.Target, c.Branch, lo, failed.detail())
	res.Hint = fmt.Sprintf("send %s back, or run the train without it; it may break only with the member(s) before it", c.Target)
	if !landGreen || lo == 0 {
		return 0, true, nil
	}
	res.Hint += fmt.Sprintf("; the verified first %d member(s) landed (--land-green)", lo)
	return lo, false, nil
}
