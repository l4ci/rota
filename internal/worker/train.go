package worker

import (
	"context"
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/change"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/flakeledger"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/land"
	"github.com/l4ci/rota/internal/landfreeze"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/testledger"
	"math/bits"
	"os"
	"slices"
	"sort"
	"strconv"
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
//     Then run each member's `## Try it` steps in that tree (#915), the first
//     failing member being the culprit as in 6.
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

// GateOrder: a shared-path member conflicts with the members before it; the CLI
// exits 4 with it, blockedBy order. Nothing was pushed.
const GateOrder = "order"

// GatePlan: the verdict of a --plan answer. Nothing was verified or landed;
// the CLI exits 0 with the conflict map.
const GatePlan = "plan"

// TrainOpts are the flags of `rota worker train`.
type TrainOpts struct {
	// NoWait is GateOpts.NoWait for an autopilot train.
	NoWait  bool
	Targets []string // slot names or PR refs, in merge order
	// Round reads the round lease for the train's ledger entries; Train makes
	// one when nil, so the whole train reads the lease once.
	Round *RoundMemo
	Base  string
	// SyncBase is the project base branch to merge into Base before the
	// members, when Base is a milestone branch; "" for a train on the base.
	SyncBase string
	// LandGreen lands the verified prefix before the culprit when the train
	// fails verification.
	LandGreen bool
	// NoVerify lets the train run with an empty test.full (it is refused
	// otherwise); a set test.full is still run.
	NoVerify bool
	// FinalSlice names the targets that are the last slice of their
	// partial-slice issue (GateOpts.FinalSlice, per member).
	FinalSlice []string
	// Approve is the merge-approval check for the whole train: files lists the
	// union of the paths the members change.
	Approve func(files func() ([]string, error)) (*exitcode.Refusal, error)
	// Verdict is GateOpts.Verdict, run for every member's check.
	Verdict func(branch, head string) error
	// CheckReviews is GateOpts.CheckReviews, run for every member's check.
	CheckReviews bool
	// Order replaces the computed merge order; see orderTrain.
	Order []string
	// Say receives each line of the order report before the scratch merge.
	Say func(string)
	// Plan previews the train: it checks the members and maps their conflicts,
	// then answers with TrainResult.Plan. No tests run, nothing is pushed and the
	// land lock is not taken.
	Plan bool
	// ItemBody is GateOpts.ItemBody for every member's behaviour run (#915);
	// nil leaves the run out.
	ItemBody func(issue string) (body string, found bool, err error)
}

// TrainMember is one target of the train, in order.
type TrainMember struct {
	Target  string
	Branch  string
	PR      string
	Tier    string // the change tier its check-only gate put it in
	Landed  bool
	Culprit bool
}

// TrainResult is the train's answer. Verdict is pass, or the verdict of the
// step that refused: a member's check verdict (stale, pr-mismatch,
// provenance-fail, ...), merge-failed, verify-failed, approval-required,
// base-moved. Culprit names the member the verdict is about, when one.
type TrainResult struct {
	Base    string
	Verdict string
	// Tier is the highest change tier of the members, which the train verified
	// by (#2348); Skipped are the test tiers it ran a cheaper list in place of.
	Tier    string
	Skipped []string
	Members []TrainMember
	Culprit string
	// Culprits lists members excluded by the static PR-section pre-filter.
	Culprits []TrainCulprit
	// CulpritGate is the gate result that named the culprit, for the bookkeeping
	// that follows (its head SHA, PR and branch). Zero when none did.
	CulpritGate GateResult
	Verified    []string            // test.full commands that passed
	Flaky       []flakeledger.Entry // commands recovered on one sequential retry
	E2EVerified []string            // test.e2e commands that passed
	Landed      []string
	Changed     bool
	SHA         string
	Err         string
	Hint        string
	// Refusal is the approval gate's refusal, set with verdict approval-required.
	Refusal *exitcode.Refusal
	Notes   []string
	Order   []string // one line per member, in merge order, with the reason
	// CacheHits names each verify answered from the verdict cache instead of
	// being run; Transient the members earlier named culprit that this train
	// passed with (#400).
	CacheHits []string
	Transient []string
	// Excluded lists the test-ledger entries that excused a failing command;
	// Expired the entries past their expiry, which fail the train.
	Excluded, Expired []testledger.Entry
	// Plan is the conflict map of a --plan run; nil otherwise.
	Plan *TrainPlan
}

// TrainPlan is a train's dry-run answer: which member pairs conflict, the
// largest conflict-free subset in merge order and the members left out.
type TrainPlan struct {
	Conflicts []TrainConflict
	// Landable lists the targets of the largest conflict-free subset, in merge order.
	Landable []string
	Blocked  []TrainBlocked
	// Command is the train line that lands exactly Landable.
	Command string
	// NoVerify mirrors the no-verify refusal: local verification with both
	// test.full and test.e2e empty. Command carries --no-verify when it is true.
	NoVerify bool
}

// TrainConflict is a pair of members whose merge conflicts.
type TrainConflict struct {
	A, B  string // targets
	Paths []string
}

// TrainBlocked is a member left out of the landable subset, with the landable
// members it conflicts with.
type TrainBlocked struct {
	Target string
	By     []string // targets
}

// OK reports a train that verified and landed every member left by the pre-filter.
func (r TrainResult) OK() bool { return r.Verdict == GatePass }

// Train runs a merge train (see above). The base must be checked out in root.
func (e Env) Train(ctx context.Context, root string, o TrainOpts) (TrainResult, error) {
	e = e.WithDefaults()
	if o.Round == nil {
		o.Round = &RoundMemo{}
	}
	var res TrainResult
	if o.Plan { // a preview changes nothing, so it does not queue behind a landing
		return e.train(ctx, root, o, nil, nil)
	}
	err := e.withLandingRun(ctx, root, strings.Join(o.Targets, ", "), o.NoWait, func(bool) (err error) {
		cache := loadTrainCache(root)
		gated := map[string]bool{}
		res, err = e.train(ctx, root, o, cache, gated)
		trainLedger(e, o.Round, root, res, gated)
		if cache.dirty {
			if serr := cache.save(); serr != nil {
				res.Notes = append(res.Notes, "TRAIN-CACHE not saved — "+serr.Error())
			}
		}
		return err
	})
	if errors.Is(err, errLandingBusy) {
		res = TrainResult{Base: o.Base, Verdict: GateRunning, Err: err.Error()}
		err = nil
	}
	return res, err
}

// trainRun is one train's state across its three stages: plan (check, order,
// pre-merge policy, scratch merge), verify (test.full, test.e2e, bisect) and
// land. Each stage returns done when res already holds the train's answer.
// rem is the configured git remote the train fetches from and pushes to.
func (t *trainRun) rem() string { return t.in.cfg.RemoteName() }

type trainRun struct {
	// tierCfg and tierRan are the tier's verify config and its list's name
	// (pickTier); nil and "" when the train verifies with test.full.
	tierCfg *GateConfig
	tierRan string
	e       Env
	ctx     context.Context
	root    string
	o       TrainOpts
	cache   *trainCache
	gated   map[string]bool // members the landing loop ran a real gate on
	res     TrainResult

	in      gateInput
	remote  bool
	onCI    bool
	verify  func(dir string) (VerifyResult, error) // test.full on dir, local or on CI
	baseRef string
	baseSHA string
	// syncSHA is the scratch tip after the project base was merged into a
	// milestone Base; "" when there was nothing to merge.
	syncSHA string
	headRef func(TrainMember) string
	heads   []string // each member's head sha, in merge order
	tips    []string // tips[i] is the scratch tree with the first i members merged
	scratch string
	cleanup func()
	// passing is how many leading members may land: all of them, or the
	// verified prefix before the culprit under LandGreen. Set by the verify stage.
	passing int
}

// gated collects the members the landing loop ran a real gate on; each of those
// wrote its own ledger entry.
func (e Env) train(ctx context.Context, root string, o TrainOpts, cache *trainCache, gated map[string]bool) (TrainResult, error) {
	t := &trainRun{e: e, ctx: ctx, root: root, o: o, cache: cache, gated: gated, res: TrainResult{Base: o.Base}}
	defer func() { // set once plan builds the scratch tree; runs on every exit, a panic included
		if t.cleanup != nil {
			t.cleanup()
		}
	}()
	if done, err := t.plan(); done || err != nil {
		return t.res, err
	}
	if done, err := t.verifyStage(); done || err != nil {
		return t.res, err
	}
	if done, err := t.vacuousStage(); done || err != nil {
		return t.res, err
	}
	if done, err := t.behaviourStage(); done || err != nil {
		return t.res, err
	}
	return t.land()
}

// broke ends the train with CHECK-BROKE: nothing has landed.
func (t *trainRun) broke(msg string) (bool, error) {
	t.res, _ = t.e.trainBroke(t.res, msg)
	return true, nil
}

// plan is stages 1 to 3: check every member, order them, run the pre-merge
// policy and merge them in a scratch worktree. The caller runs t.cleanup, once
// it is set, on every exit.
func (t *trainRun) plan() (bool, error) {
	e, ctx, root, o, res := t.e, t.ctx, t.root, t.o, &t.res
	if len(o.Targets) == 0 {
		return true, fail(exitcode.ExitUsage, "a train needs at least one PR or slot")
	}
	reg, err := LoadRegistry(root)
	if err != nil {
		return true, err
	}
	if !reg.Exists {
		return true, fail(exitcode.ExitResolution, "no worker pool — run rota worker pool init first")
	}
	if _, code := e.git(root, "rev-parse", "--verify", "--quiet", o.Base); code != 0 {
		return true, fail(exitcode.ExitResolution, fmt.Sprintf("base branch '%s' does not exist", o.Base))
	}
	if cur, _ := e.git(root, "rev-parse", "--abbrev-ref", "HEAD"); cur != o.Base {
		return true, fail(exitcode.ExitResolution, fmt.Sprintf("a train must run with %s checked out (currently on %s)", o.Base, cur))
	}
	// A bad test.fullWhere, test.ciChecks or ledger is refused before any
	// member is checked, and the train carries this read through.
	in, err := e.GateInput(root)
	if err != nil {
		return true, err
	}
	in.cfg.WarnLegacy(e.Stderr)
	if err := in.cfg.RemoteErr; err != nil {
		return true, fail(exitcode.ExitRefused, err.Error())
	}
	t.in = in
	seen := map[string]bool{}
	withPR := 0
	for _, tg := range o.Targets {
		gt, err := reg.GateTargetAny(tg)
		if err != nil {
			return true, err
		}
		key := gt.Branch + "|" + gt.PR
		if gt.PR != "" {
			// External targets keep the caller's spelling until the gate reads
			// the forge: 7, #7 and its URL still name the same train member.
			key = "pr|" + prNumText(gt.PR)
		}
		if seen[key] {
			return true, fail(exitcode.ExitUsage, fmt.Sprintf("%s names a PR already in the train", tg))
		}
		seen[key] = true
		if gt.PR != "" {
			withPR++
		}
	}
	if withPR != 0 && withPR != len(o.Targets) {
		return true, fail(exitcode.ExitUsage, fmt.Sprintf("a train is all PRs or all slots without one, not a mix: PRs merge onto %s/%s and slots onto the local %s", t.rem(), o.Base, o.Base))
	}

	// Freeze is a queue-wide refusal, before static sections can prune members.
	frozen, err := landfreeze.Load(ctx, e.Git, root)
	if err != nil {
		res.Verdict, res.Err, res.Hint = GateFreeze, landfreeze.UnreadableLine(err), landfreeze.ClearHint
		return true, nil
	}
	if frozen.Frozen() {
		if !frozen.Binds(o.Base) {
			res.Verdict, res.Err, res.Hint = GateFreeze, frozen.Line(), "repairs must target the frozen base"
			return true, nil
		}
		for _, tg := range o.Targets {
			gt, err := reg.GateTargetAny(tg)
			if err != nil {
				return true, err
			}
			if frozen.Allows(gt.Issue, gt.PR) {
				continue
			}
			// A PR outside the registry derives its repair issue from its head branch.
			if gt.External {
				gr, err := e.Gate(ctx, root, GateOpts{Slot: tg, Base: o.Base, CheckOnly: true})
				if err != nil {
					return true, err
				}
				if gr.Verdict != GateFreeze {
					continue
				}
			}
			res.Verdict, res.Err, res.Hint = GateFreeze, frozen.Line(), "designate the repair with rota round freeze before landing or assigning"
			return true, nil
		}
	}

	// 1. Pre-filter static PR sections, then check the remaining members.
	targets, done, err := t.filterPRSections(reg, o.Targets)
	if done || err != nil {
		return true, err
	}
	for _, tg := range targets {
		gr, err := e.Gate(ctx, root, GateOpts{Slot: tg, Base: o.Base, CheckOnly: true, FinalSlice: slices.Contains(o.FinalSlice, tg), Verdict: o.Verdict, CheckReviews: o.CheckReviews})
		if err != nil {
			if gr.Verdict == GateVerdictBlocked {
				res.Verdict, res.Culprit = gr.Verdict, tg
			}
			return true, err
		}
		res.Notes = appendNew(res.Notes, gr.Notes...)
		if t.excludePRSections(tg, gr) {
			continue
		}
		if !gr.OK() {
			res.Verdict, res.Culprit, res.CulpritGate, res.Err, res.Hint = gr.Verdict, tg, gr, gr.Err, gr.Hint
			return true, nil
		}
		res.Members = append(res.Members, TrainMember{Target: tg, Branch: gr.Branch, PR: gr.PR, Tier: gr.Tier})
		t.remote = t.remote || gr.PR != ""
	}
	if err := t.filterSectionOrder(&o); err != nil {
		return true, err
	}
	if len(res.Members) == 0 {
		c := res.Culprits[0]
		res.Verdict, res.Culprit, res.CulpritGate, res.Err, res.Hint = c.Gate.Verdict, c.Target, c.Gate, c.Gate.Err, c.Gate.Hint
		return true, nil
	}
	t.baseRef = o.Base
	if t.remote {
		t.baseRef = t.rem() + "/" + o.Base
	}
	t.headRef = func(m TrainMember) string {
		if m.PR != "" {
			return t.rem() + "/" + m.Branch
		}
		return m.Branch
	}
	baseRef, headRef := t.baseRef, t.headRef
	if o.Plan {
		return t.preview()
	}
	verify, onCI, brokeMsg, err := e.fullTier(ctx, root, "train", in)
	if err != nil {
		return true, err
	}
	if brokeMsg != "" {
		return t.broke(brokeMsg)
	}
	t.verify, t.onCI = verify, onCI

	// Order the members to minimise predicted conflicts, or take --order.
	foots, err := e.trainFootprints(root, baseRef, res.Members, headRef)
	if err != nil {
		return t.broke(err.Error())
	}
	perm, lines, nIso, err := orderTrain(res.Members, foots, o.Order)
	if err != nil {
		return true, err
	}
	autoOrder := len(o.Order) == 0
	ms := make([]TrainMember, len(perm))
	fs := make([]trainFoot, len(perm))
	ls := make([]string, len(perm))
	for i, j := range perm {
		ms[i], fs[i], ls[i] = res.Members[j], foots[j], lines[j]
	}
	res.Members, foots = ms, fs
	res.Order = ls
	if o.Say != nil {
		for _, l := range ls {
			o.Say("ORDER " + l)
		}
	}

	// The union of the files the members change.
	files := func() ([]string, error) {
		set := map[string]bool{}
		for _, f := range foots {
			for _, p := range f.paths {
				set[p] = true
			}
		}
		list := make([]string, 0, len(set))
		for f := range set {
			list = append(list, f)
		}
		sort.Strings(list)
		return list, nil
	}
	// 2. The pre-merge policy the gate shares (premerge.go): an expired ledger
	// entry fails the train, an empty test.full would land it unverified, a CI
	// definition change needs a human, and one approval covers the train. Each
	// refuses before the scratch tree is built.
	pre := premergeRun{
		Subject:      trainSubject,
		In:           in,
		Now:          e.Now(),
		SkipNoVerify: o.NoVerify,
		Files:        files,
	}
	if onCI {
		pre.CIChanged = func() ([]string, error) {
			var changed []string
			for _, m := range res.Members {
				f, err := e.ciDiffFiles(ctx, root, baseRef, headRef(m))
				if err != nil {
					return nil, err
				}
				changed = append(changed, f...)
			}
			return changed, nil
		}
	}
	pre.Approve = o.Approve
	r, approval, ref, perr := pre.run()
	switch {
	case approval:
		res.Verdict = GateApprovalRequired
		res.Refusal = ref
		if ref != nil {
			perr = ref.Err
		}
		return true, perr
	case perr != nil:
		return t.broke(perr.Error())
	case r != nil:
		res.Verdict, res.Err, res.Hint, res.Expired = r.Verdict, r.Err, r.Hint, r.Expired
		return true, nil
	}

	// 3. Merge in order in a scratch worktree.
	if o.SyncBase != "" && t.remote {
		if _, code := e.git(root, "fetch", t.rem(), "-q"); code != 0 {
			return t.broke("git fetch " + t.rem() + " failed before syncing " + o.SyncBase)
		}
	}
	baseSHA, code := e.git(root, "rev-parse", baseRef)
	if code != 0 {
		return t.broke(fmt.Sprintf("git rev-parse %s exited %d", baseRef, code))
	}
	t.baseSHA = baseSHA
	t.heads = make([]string, len(res.Members))
	for i, m := range res.Members {
		if t.heads[i], code = e.git(root, "rev-parse", headRef(m)); code != 0 {
			return t.broke(fmt.Sprintf("git rev-parse %s exited %d", headRef(m), code))
		}
	}
	t.pickTier()
	t.cache.retainBase(baseSHA)
	scratch, cleanup, err := e.scratchTree(ctx, root, baseSHA, "rota-train-")
	if err != nil {
		return t.broke(err.Error())
	}
	t.scratch, t.cleanup = scratch, cleanup
	if o.SyncBase != "" {
		if done, err := t.syncBase(); done || err != nil {
			return done, err
		}
	}
	tips, failed, merr, brokeMsg := e.mergeTrain(ctx, scratch, o.Base, res.Members, t.heads)
	t.tips = tips
	if brokeMsg != "" {
		return t.broke(brokeMsg)
	}
	if merr != nil && autoOrder && failed >= nIso && failed > 0 && errors.As(merr, new(*land.ConflictError)) {
		// A shared-path member conflicts with the ones before it. Git's pairwise
		// conflicts do not depend on merge order, so no other order is tried.
		res.Verdict, res.Culprit = GateOrder, res.Members[failed].Target
		res.Members[failed].Culprit = true
		res.Err = fmt.Sprintf("TRAIN-BLOCKED order — %s; the shared-path members conflict in the order tried (%s); nothing pushed", conflictPair(res.Members, foots, failed), orderText(res.Members))
		res.Hint = "rebase one of the pair on the other's merge, or land them in separate trains, or pass --order to try another"
		return true, nil
	}
	if merr != nil {
		m := res.Members[failed]
		res.Culprit = m.Target
		res.Members[failed].Culprit = true
		res.Verdict = GateMergeFailed
		res.Err, res.Hint = mergeRefusal(merr, mergeWords{
			Failed:       fmt.Sprintf("TRAIN-FAIL %s — merging %s into the scratch tree failed", m.Target, m.Branch),
			Conflict:     fmt.Sprintf("TRAIN-FAIL %s — %s does not merge onto %s with the %d member(s) before it: conflict", m.Target, m.Branch, o.Base, failed),
			ConflictHint: fmt.Sprintf("send %s back to merge %s, or run the train without it", m.Target, o.Base),
		})
		return true, nil
	}
	return false, nil
}

// syncBase merges the project base into the milestone Base in the scratch tree,
// before the members, so the verify runs on base + milestone + members. A
// conflict ends the train with merge-failed naming the milestone and the
// paths; nothing has been pushed at this point.
func (t *trainRun) syncBase() (bool, error) {
	e, ctx, o, res := t.e, t.ctx, t.o, &t.res
	from := o.SyncBase
	if t.remote {
		from = t.rem() + "/" + o.SyncBase
	}
	sha, code := e.git(t.root, "rev-parse", from)
	if code != 0 {
		return t.broke(fmt.Sprintf("git rev-parse %s exited %d", from, code))
	}
	run := func(args ...string) (git.Result, error) { return e.Git(ctx, t.scratch, args...) }
	if err := land.MergeLocal(run, sha, fmt.Sprintf("sync %s into %s", o.SyncBase, o.Base), land.RecoveryGit(ctx, e.Git, t.scratch)); err != nil {
		var ce *land.ConflictError
		if errors.As(err, &ce) {
			res.Verdict = GateMergeFailed
			res.Err = fmt.Sprintf("TRAIN-FAIL sync — merging %s into %s conflicts in: %s; nothing pushed", o.SyncBase, o.Base, strings.Join(conflictPaths(ce.Out), ", "))
			res.Hint = fmt.Sprintf("merge %s into %s by hand, or file a fix issue tagged to the milestone, then re-run the train", o.SyncBase, o.Base)
			return true, nil
		}
		res.Verdict = GateMergeFailed
		res.Err, res.Hint = mergeRefusal(err, mergeWords{Failed: fmt.Sprintf("TRAIN-FAIL sync — merging %s into %s in the scratch tree failed", o.SyncBase, o.Base)})
		return true, nil
	}
	tip, code := e.git(t.scratch, "rev-parse", "HEAD")
	if code != 0 {
		return t.broke("git rev-parse HEAD failed in the scratch tree")
	}
	if tip != t.baseSHA {
		t.syncSHA = tip
		t.cache.retainBase(tip)
	}
	return false, nil
}

// conflictPaths lists the paths git named in a merge's "CONFLICT (...)" lines.
func conflictPaths(out string) []string {
	var paths []string
	for _, l := range strings.Split(out, "\n") {
		if !strings.HasPrefix(l, "CONFLICT") {
			continue
		}
		if i := strings.LastIndex(l, " in "); i >= 0 && strings.Contains(l, "Merge conflict") {
			paths = append(paths, strings.TrimSpace(l[i+4:]))
		} else if i := strings.Index(l, "): "); i >= 0 { // modify/delete, rename and add/add forms
			paths = append(paths, strings.TrimSpace(l[i+3:]))
		}
	}
	if len(paths) == 0 {
		return []string{"(see git merge output)"}
	}
	return paths
}

// pushSync lands the verified sync merge on the milestone Base ahead of the
// first member: a fast-forward push to origin for a PR train, a ff-only merge
// of the checked-out Base for a slot train.
func (t *trainRun) pushSync() error {
	e, root, o := t.e, t.root, t.o
	if t.remote {
		if out, code := e.runGit(t.ctx, root, "push", "-q", t.rem(), t.syncSHA+":refs/heads/"+o.Base); code != 0 {
			return fmt.Errorf("git push of the sync merge to %s/%s failed: %s", t.rem(), o.Base, strings.TrimSpace(out))
		}
		if _, code := e.git(root, "fetch", t.rem(), "-q"); code != 0 {
			return errors.New("git fetch " + t.rem() + " failed after pushing the sync merge")
		}
		return nil
	}
	if out, code := e.runGit(t.ctx, root, "merge", "--ff-only", "-q", t.syncSHA); code != 0 {
		return fmt.Errorf("fast-forwarding %s to the sync merge failed: %s", o.Base, strings.TrimSpace(out))
	}
	return nil
}

// preview answers --plan: the pairs of members that really conflict, the
// largest conflict-free subset in merge order and the members left out. It runs
// no tests and touches no ref. Only pairs sharing a path are merge-tried, with
// git merge-tree; three-way interactions between members are not looked for.
func (t *trainRun) preview() (bool, error) {
	e, root, res := t.e, t.root, &t.res
	foots, err := e.trainFootprints(root, t.baseRef, res.Members, t.headRef)
	if err != nil {
		return t.broke(err.Error())
	}
	n := len(res.Members)
	clash := make([][]bool, n)
	for i := range clash {
		clash[i] = make([]bool, n)
	}
	plan := &TrainPlan{}
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			both := pairwisePaths(foots[i].paths, foots[j].paths, nil)
			if len(both) == 0 {
				continue
			}
			conflict, _, code := pairwiseMergeConflict(e.git, root, t.headRef(res.Members[i]), t.headRef(res.Members[j]))
			if code != 0 {
				return t.broke(fmt.Sprintf("git merge-tree of %s and %s exited %d", memberLabel(res.Members[i]), memberLabel(res.Members[j]), code))
			}
			if conflict {
				clash[i][j], clash[j][i] = true, true
				plan.Conflicts = append(plan.Conflicts, TrainConflict{A: res.Members[i].Target, B: res.Members[j].Target, Paths: both})
			}
		}
	}
	keep := largestFree(clash)
	var sub []TrainMember
	var subFoots []trainFoot
	for _, i := range keep {
		sub, subFoots = append(sub, res.Members[i]), append(subFoots, foots[i])
	}
	perm, _, _, err := orderTrain(sub, subFoots, nil)
	if err != nil {
		return true, err
	}
	inSub := map[int]bool{}
	for _, i := range keep {
		inSub[i] = true
	}
	for _, j := range perm {
		plan.Landable = append(plan.Landable, sub[j].Target)
	}
	for i, m := range res.Members {
		if inSub[i] {
			continue
		}
		b := TrainBlocked{Target: m.Target}
		for _, k := range keep {
			if clash[i][k] {
				b.By = append(b.By, res.Members[k].Target)
			}
		}
		plan.Blocked = append(plan.Blocked, b)
	}
	if len(plan.Landable) > 0 {
		plan.Command = fmt.Sprintf("rota worker train --base %s --order %s %s", t.o.Base, strings.Join(plan.Landable, ","), strings.Join(plan.Landable, " "))
		if plan.NoVerify = noVerifyRule(t.in.cfg.where, t.in.cfg.Full, t.in.cfg.E2E); plan.NoVerify {
			plan.Command += " --no-verify"
		}
	}
	res.Plan, res.Verdict = plan, GatePlan
	return true, nil
}

// largestFree returns the indices of a largest set of members no two of which
// clash, the earliest such set when several tie. Exhaustive up to 20 members,
// greedy (fewest clashes first) beyond that.
func largestFree(clash [][]bool) []int {
	n := len(clash)
	if n > 20 {
		var keep []int
		for i := 0; i < n; i++ {
			ok := true
			for _, k := range keep {
				ok = ok && !clash[i][k]
			}
			if ok {
				keep = append(keep, i)
			}
		}
		return keep
	}
	best, bestN := uint32(0), -1
	for mask := uint32(0); mask < 1<<n; mask++ {
		if c := bits.OnesCount32(mask); c > bestN && free(clash, mask) {
			best, bestN = mask, c
		}
	}
	var keep []int
	for i := 0; i < n; i++ {
		if best&(1<<i) != 0 {
			keep = append(keep, i)
		}
	}
	return keep
}

func free(clash [][]bool, mask uint32) bool {
	for i := range clash {
		for j := i + 1; j < len(clash); j++ {
			if mask&(1<<i) != 0 && mask&(1<<j) != 0 && clash[i][j] {
				return false
			}
		}
	}
	return true
}

// pickTier sets what the train verifies with (#2348): the tier of the whole
// train, so one full member makes it full. It runs once the heads are pinned
// and classifies the union of the files each member's base...head changes, with
// their changed lines summed, once; it takes the higher of that and the tier
// each member's check-only gate saw (which read the PR's Door line): a member
// pushed again since its check is never verified by a stale tier. A member with no tier counts as full, and so does a train that
// syncs another base in, whose code no member's diff shows. Under
// test.fullWhere ci the tier is recorded, not applied. For test.small, {files}
// is the union of the members' changed files, deleted ones included.
func (t *trainRun) pickTier() {
	res := &t.res
	tier := change.TierDocs
	if len(res.Members) == 0 || t.o.SyncBase != "" {
		tier = change.TierFull
	}
	policy, perr := t.in.cfg.ChangePolicy()
	var files []string
	lines := 0
	seen := map[string]bool{}
	for i, m := range res.Members {
		tier = change.Higher(tier, change.Tier(m.Tier))
		if tier == change.TierFull {
			break
		}
		span := t.baseSHA + "..." + t.heads[i]
		fs, err := git.DiffFiles(t.ctx, t.e.Git, t.root, span)
		if err != nil {
			tier = change.TierFull
			break
		}
		n, counted, err := git.ChangedLinesCounted(t.ctx, t.e.Git, t.root, span)
		if err != nil || !counted {
			lines = -1 // an unknown count is never small
		} else if lines >= 0 {
			lines += n
		}
		for _, f := range fs {
			if !seen[f] {
				seen[f] = true
				files = append(files, f)
			}
		}
	}
	if tier != change.TierFull {
		// One classification of the union, lines summed: three 90-line members
		// are 270 lines together, not three small ones.
		var now change.Result
		if perr != nil {
			now = change.Result{Tier: change.TierFull}
		} else {
			now = change.Classify(change.Input{Files: files, Lines: lines}, policy)
		}
		tier = change.Higher(tier, now.Tier)
	}
	res.Tier = string(tier)
	if tier == change.TierFull || t.onCI {
		return
	}
	cfg, ran, skipped := t.in.cfg.forTier(tier, files)
	if len(skipped) == 0 {
		return
	}
	t.tierCfg, t.tierRan, res.Skipped = &cfg, ran, skipped
	ctx, e, ledger := t.ctx, t.e, t.in.ledger
	t.verify = func(dir string) (VerifyResult, error) { return e.runGateVerify(ctx, cfg.Full, dir, ledger) }
	res.Notes = append(res.Notes, fmt.Sprintf("GATE-TIER train — %s (every member is %s or less); ran %s; skipped %s", tier, tier, ran, strings.Join(skipped, ", ")))
}

// verifyCfg is the config the train's scratch tree verifies with: the tier's
// (pickTier), else test.full and test.e2e.
func (t *trainRun) verifyCfg() GateConfig {
	if t.tierCfg != nil {
		return *t.tierCfg
	}
	return t.in.cfg
}

// verifyStage is stage 4: test.full, then test.e2e on the same tree, with a
// bisect on a red tier. It sets t.passing. The tier policy (what a result means,
// the note text) is tierOutcomeOf's; this stage owns the cache and the bisect.
func (t *trainRun) verifyStage() (bool, error) {
	e, ctx, o, res, scratch, tips, heads := t.e, t.ctx, t.o, &t.res, t.scratch, t.tips, t.heads
	cache, baseSHA := t.cache, t.baseSHA
	if t.syncSHA != "" { // the verified tree includes the sync merge
		baseSHA = t.syncSHA
	}
	n := len(res.Members)
	passing := n
	t.passing = n
	// A local run that cannot start is an error, as before; a CI push or
	// forge read that fails is CHECK-BROKE, since nothing has landed.
	verifyErr := func(err error) (bool, error) {
		if t.onCI {
			return t.broke(err.Error())
		}
		return true, err
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
				prs := []string{}
				for _, m := range res.Members[:k] {
					if m.PR != "" {
						prs = append(prs, m.PR)
					}
				}
				err = e.recordFlakes(t.root, o.Round, prs, r)
				// Prefix verifies run inside bisect, outside the tier walk.
				if k != n {
					for _, f := range r.Flaky {
						res.Flaky = append(res.Flaky, f)
						res.Notes = append(res.Notes, flakeNote(f))
					}
				}
			}
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
	fullTierName := tierFull
	if t.tierRan != "" {
		fullTierName = t.tierRan // a cached test.full result never answers a test.docs run
	}
	if t.onCI {
		fullTierName = tierFull + "@ci"
	}
	full := cached(fullTierName, func() (VerifyResult, error) { return t.verify(scratch) })
	e2e := cached(tierE2E, func() (VerifyResult, error) { return e.runE2E(ctx, t.in.cfg.E2E, scratch, t.in.ledger) })
	// bisect names the culprit of a red tier; it reports whether the train ends.
	bisect := func(tier string, run func(int) (VerifyResult, error), failed VerifyResult, key string) (stop bool, err error) {
		passing, stop, err = e.bisectTrain(scratch, res, tips, tier, run, failed, o.LandGreen)
		blame(key)
		return stop, err
	}
	// 4b. E2E: the most expensive tier runs once, on the train result, after
	// test.full passed. A red e2e bisects the same way a red full does.
	w := &tierWalk{
		subject: trainSubject.CIWho,
		run: func(tier string, _ []string) (VerifyResult, error) {
			if tier == tierFull {
				return full(n)
			}
			return e2e(n)
		},
		stop: func(vr VerifyResult) bool {
			r, done := trainStopped(*res, vr)
			if done {
				t.res = r
			}
			return done
		},
		red: func(tier string, vr VerifyResult) (bool, error) {
			if tier == tierFull {
				return bisect(tierFull, full, vr, fullTierName)
			}
			return bisect(tierE2E, e2e, vr, tierE2E)
		},
		notes: &res.Notes, verified: &res.Verified, e2eVerified: &res.E2EVerified, excluded: &res.Excluded, flaky: &res.Flaky,
	}
	ended, err := w.walk(t.verifyCfg(), tierFull)
	if err != nil {
		if w.at == tierFull {
			return verifyErr(err)
		}
		return true, err
	}
	if ended {
		return true, nil
	}
	green := !w.fullRed

	// A member earlier named culprit that this verified train carries was flaky:
	// the failure did not reproduce on a different combination.
	if passing == n && !w.fullEmpty {
		for i, m := range res.Members {
			if cache.forgive(m.Branch, heads[i]) {
				res.Transient = append(res.Transient, m.Target)
				res.Notes = append(res.Notes, fmt.Sprintf("TRANSIENT %s — named culprit by an earlier train, but this train with it passes verification; treated as flaky, not blamed again", m.Target))
			}
		}
	}

	if !green && passing > 0 && len(t.verifyCfg().E2E) > 0 {
		res.Notes = append(res.Notes, noE2ENote(passing))
	}
	t.passing = passing
	return false, nil
}

// behaviourStage runs each member's `## Try it` steps (#915), after test.full
// and test.e2e passed and before anything lands. A member's steps run in the
// scratch tree with the members up to it merged, through a CheckOnly gate, so
// the collaborator trust check, the scrubbed environment, the transcript, the
// behaviour verdict and the PR comment are the gate's own. The first failing
// member is the culprit: nothing lands, or under LandGreen the members before
// it do. It lowers t.passing; it is a no-op without ItemBody.
func (t *trainRun) behaviourStage() (bool, error) {
	e, ctx, root, o, res := t.e, t.ctx, t.root, t.o, &t.res
	if o.ItemBody == nil {
		return false, nil
	}
	for i := 0; i < t.passing; i++ {
		m := res.Members[i]
		// Files an earlier member's steps wrote must not reach this member's run.
		if _, code := e.git(t.scratch, "clean", "-q", "-fdx"); code != 0 {
			return t.broke("git clean failed in the scratch tree")
		}
		if _, code := e.git(t.scratch, "checkout", "-q", "-f", "--detach", t.tips[i+1]); code != 0 {
			return t.broke("git checkout " + t.tips[i+1] + " failed in the scratch tree")
		}
		gr, err := e.Gate(ctx, root, GateOpts{Slot: m.Target, Base: o.Base, CheckOnly: true, FinalSlice: slices.Contains(o.FinalSlice, m.Target), Verdict: o.Verdict, CheckReviews: o.CheckReviews,
			ItemBody: o.ItemBody, BehaviourDir: t.scratch})
		if err != nil {
			return true, err
		}
		res.Notes = appendNew(res.Notes, gr.Notes...)
		// The gate read the head again; its verdict and comment belong to the
		// head the steps ran on, so a head that moved since plan ends the train.
		if cur, _ := e.git(root, "rev-parse", t.headRef(m)); cur != t.heads[i] {
			t.res, _ = e.trainMoved(*res, m.Target, fmt.Sprintf("%s moved from %s to %s while the train ran its Try it steps", t.headRef(m), strutil.ShortSHA(t.heads[i]), strutil.ShortSHA(cur)))
			return true, nil
		}
		if gr.OK() {
			continue
		}
		for j := range res.Members { // an earlier bisect culprit is outside the landed range now
			res.Members[j].Culprit = false
		}
		res.Members[i].Culprit = true
		res.Verdict, res.Culprit, res.CulpritGate, res.Err = gr.Verdict, m.Target, gr, gr.Err
		res.Hint = gr.Hint
		if !o.LandGreen || i == 0 {
			return true, nil
		}
		res.Hint = strings.TrimSpace(fmt.Sprintf("%s; the verified first %d member(s) landed (--land-green)", res.Hint, i))
		t.passing = i
		return false, nil
	}
	return false, nil
}

// land is stage 5. The tree that was verified must still be the one that lands.
func (t *trainRun) land() (TrainResult, error) {
	e, ctx, root, o, tips, heads := t.e, t.ctx, t.root, t.o, t.tips, t.heads
	res, baseRef, baseSHA, headRef, passing := t.res, t.baseRef, t.baseSHA, t.headRef, t.passing
	n := len(res.Members)
	if t.remote {
		if _, code := e.git(root, "fetch", t.rem(), "-q"); code != 0 {
			return e.trainBroke(res, "git fetch "+t.rem()+" failed before landing")
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
	if passing > 0 && t.syncSHA != "" {
		if err := t.pushSync(); err != nil {
			return e.trainMoved(res, "", err.Error())
		}
	}
	for i := 0; i < passing; i++ {
		m := res.Members[i]
		if i > 0 { // each landing must start from exactly the tree the scratch merge had at that point
			if t.remote {
				if _, code := e.git(root, "fetch", t.rem(), "-q"); code != 0 {
					return e.trainBroke(res, "git fetch "+t.rem()+" failed between landings")
				}
			}
			got, _ := e.git(root, "rev-parse", baseRef+"^{tree}")
			if want, _ := e.git(root, "rev-parse", tips[i]+"^{tree}"); got != want {
				return e.trainMoved(res, m.Target, fmt.Sprintf("%s changed outside the train after %d landing(s) (tree %s, verified %s)", baseRef, i, strutil.ShortSHA(got), strutil.ShortSHA(want)))
			}
		}
		t.gated[m.Target] = true
		gr, err := e.Gate(ctx, root, GateOpts{Slot: m.Target, Base: o.Base, LandVerified: true, FinalSlice: slices.Contains(o.FinalSlice, m.Target), HoldsLandLock: true, Round: o.Round})
		res.Notes = appendNew(res.Notes, gr.Notes...)
		res.Notes = appendNew(res.Notes, gr.Warnings...)
		res.Changed = res.Changed || gr.Changed
		if gr.Changed && gr.Verdict != GatePass { // the PR is on the base but the gate could not finish (merged-remotely)
			res.Landed = append(res.Landed, m.Target)
			res.Members[i].Landed = true
		}
		if err != nil {
			return res, err
		}
		if !gr.OK() {
			res.Verdict, res.Culprit, res.CulpritGate, res.Err, res.Hint = gr.Verdict, m.Target, gr, gr.Err, gr.Hint
			res.Hint = strings.TrimSpace(fmt.Sprintf("landed %d of %d member(s) before this; %s", len(res.Landed), n, res.Hint))
			return res, nil
		}
		res.Landed = append(res.Landed, m.Target)
		res.Members[i].Landed = true
	}
	res.SHA, _ = e.git(root, "rev-parse", "--short=7", "HEAD")
	if tree, _ := e.git(root, "rev-parse", "HEAD^{tree}"); tree != "" {
		if want, _ := e.git(root, "rev-parse", tips[passing]+"^{tree}"); want != tree {
			// A forge may land a different tree than the scratch merge. Verify that
			// landed base before reporting the mismatch, freezing it if it is red.
			if stop, err := t.verifyLanded(&res); stop || err != nil {
				return res, err
			}
			return e.trainMoved(res, "", fmt.Sprintf("the landed tree %s differs from the verified scratch tree %s; the forge merged differently than git did", strutil.ShortSHA(tree), strutil.ShortSHA(want)))
		}
	}
	res.Verdict, res.Culprit = verdict, culprit
	if verdict == "" {
		res.Verdict = GatePass
	}
	if res.Verdict == GatePass && passing == len(res.Members) && passing > 0 && len(res.Verified) > 0 {
		if err := e.clearVerifiedFreeze(ctx, root, o.Base); err != nil {
			return res, err
		}
	}
	return res, nil
}

// mergeTrain merges heads in order onto the scratch tree. tips[i] is the tree
// with the first i members merged. A failed merge returns its error and the
// member's position; brokeMsg is set when git itself failed.
func (e Env) mergeTrain(ctx context.Context, scratch, base string, ms []TrainMember, heads []string) (tips []string, failed int, merr error, brokeMsg string) {
	tips = make([]string, len(ms)+1)
	tips[0], _ = e.git(scratch, "rev-parse", "HEAD")
	for i, m := range ms {
		run := func(args ...string) (git.Result, error) { return e.Git(ctx, scratch, args...) }
		if err := land.MergeLocal(run, heads[i], fmt.Sprintf("train: %s into %s", m.Branch, base), land.RecoveryGit(ctx, e.Git, scratch)); err != nil {
			return tips, i, err, ""
		}
		var code int
		if tips[i+1], code = e.git(scratch, "rev-parse", "HEAD"); code != 0 {
			return tips, 0, nil, "git rev-parse HEAD failed in the scratch tree"
		}
	}
	return tips, 0, nil, ""
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

// trainFoot is what one member changes against the base: the paths (read as the
// overlap check reads them, `git diff --name-only base...head`) and the number
// of changed lines.
type trainFoot struct {
	paths []string
	lines int
}

func (e Env) trainFootprints(root, baseRef string, ms []TrainMember, headRef func(TrainMember) string) ([]trainFoot, error) {
	foots := make([]trainFoot, len(ms))
	for i, m := range ms {
		span := baseRef + "..." + headRef(m)
		paths, err := git.ChangedPaths(e.Ctx, e.Git, root, span)
		if err != nil {
			return nil, err
		}
		lines, err := git.ChangedLines(e.Ctx, e.Git, root, span)
		if err != nil {
			return nil, err
		}
		foots[i] = trainFoot{paths: paths, lines: lines}
	}
	return foots, nil
}

// memberLabel names a member in the order report: its PR number, else its target.
func memberLabel(m TrainMember) string {
	if n := prNumText(m.PR); n != "" {
		return "#" + n
	}
	return m.Target
}

func orderText(ms []TrainMember) string {
	ls := make([]string, len(ms))
	for i, m := range ms {
		ls[i] = memberLabel(m)
	}
	return strings.Join(ls, ", ")
}

// orderTrain picks the merge order. perm lists positions of ms in merge order;
// lines[i] is the report line of ms[i]; nIso is how many leading members of the
// order share no path with another (the rest are the shared-path group).
//
// Without an override, members sharing no path with any other go first in PR
// number order, then the rest by ascending changed lines. With one, order names
// every member exactly once, by target, branch or PR (`12`, `#12`, URL).
func orderTrain(ms []TrainMember, foots []trainFoot, override []string) (perm []int, lines []string, nIso int, err error) {
	lines = make([]string, len(ms))
	var iso, shared []int
	for i := range ms {
		var peers []string
		n := map[string]bool{}
		for j := range ms {
			if j == i {
				continue
			}
			both := pairwisePaths(foots[i].paths, foots[j].paths, nil)
			if len(both) > 0 {
				peers = append(peers, memberLabel(ms[j]))
				for _, p := range both {
					n[p] = true
				}
			}
		}
		if len(peers) == 0 {
			lines[i] = memberLabel(ms[i]) + ": no shared paths"
			iso = append(iso, i)
		} else {
			lines[i] = fmt.Sprintf("%s: shares %d paths with %s", memberLabel(ms[i]), len(n), strings.Join(peers, ", "))
			shared = append(shared, i)
		}
	}
	prNum := func(i int) int {
		n, _ := strconv.Atoi(prNumText(ms[i].PR))
		return n
	}
	if len(override) > 0 {
		if len(override) != len(ms) {
			return nil, nil, 0, fail(exitcode.ExitUsage, fmt.Sprintf("--order names %d of the %d members; it must name each exactly once", len(override), len(ms)))
		}
		used := map[int]bool{}
		for _, tok := range override {
			at := -1
			for i, m := range ms {
				if tok == m.Target || tok == m.Branch || (prNum(i) != 0 && prNumText(tok) == prNumText(m.PR)) {
					at = i
					break
				}
			}
			switch {
			case at < 0:
				return nil, nil, 0, fail(exitcode.ExitUsage, fmt.Sprintf("--order names %s, which is not a member of the train", tok))
			case used[at]:
				return nil, nil, 0, fail(exitcode.ExitUsage, fmt.Sprintf("--order names %s twice", tok))
			}
			used[at] = true
			perm = append(perm, at)
		}
		for i := range lines {
			lines[i] += " (order given)"
		}
		return perm, lines, 0, nil
	}
	byPR := func(s []int) {
		sort.SliceStable(s, func(a, b int) bool { return prNum(s[a]) < prNum(s[b]) })
	}
	byPR(iso)
	sort.SliceStable(shared, func(a, b int) bool {
		if la, lb := foots[shared[a]].lines, foots[shared[b]].lines; la != lb {
			return la < lb
		}
		return prNum(shared[a]) < prNum(shared[b])
	})
	return append(iso, shared...), lines, len(iso), nil
}

// conflictPair names the member at failed and the earlier member it shares a
// path with, the likeliest cause of its conflict.
func conflictPair(ms []TrainMember, foots []trainFoot, failed int) string {
	for k := 0; k < failed; k++ {
		if len(pairwisePaths(foots[failed].paths, foots[k].paths, nil)) > 0 {
			return fmt.Sprintf("%s conflicts with %s", memberLabel(ms[failed]), memberLabel(ms[k]))
		}
	}
	return fmt.Sprintf("%s conflicts with the members before it", memberLabel(ms[failed]))
}

// appendNew adds the notes dst does not hold yet: the train gates a member in
// every stage, and a note the gate raises each time (CLOSING-REFS-UNKNOWN) is
// said once.
func appendNew(dst []string, notes ...string) []string {
	for _, n := range notes {
		if !slices.Contains(dst, n) {
			dst = append(dst, n)
		}
	}
	return dst
}

// landedPRs names the landed members by PR, falling back to the branch.
func landedPRs(res *TrainResult) []string {
	var prs []string
	for _, m := range res.Members {
		if m.Landed {
			pr := m.PR
			if pr == "" {
				pr = m.Branch
			}
			prs = append(prs, pr)
		}
	}
	return prs
}

// verifyLanded checks an unexpected forge result. Prefix bisection failures
// before landing never call this, and therefore never freeze the queue.
func (t *trainRun) verifyLanded(res *TrainResult) (bool, error) {
	if !t.onCI && t.o.NoVerify && len(t.in.cfg.Full) == 0 && len(t.in.cfg.E2E) == 0 {
		return false, nil
	}
	full, err := t.verify(t.root)
	if err != nil {
		return true, err
	}
	if v, msg, hint := full.stopVerdict("train"); v != "" {
		res.Verdict = v
		res.Err = strings.Replace(msg, "; nothing landed", "; train already landed; verification incomplete", 1)
		res.Hint = hint
		return true, nil
	}
	checks := full.Failed
	if full.OK() {
		e2e, err := t.e.runE2E(t.ctx, t.in.cfg.E2E, t.root, t.in.ledger)
		if err == nil {
			err = t.e.recordFlakes(t.root, t.o.Round, landedPRs(res), e2e)
		}
		if err != nil {
			return true, err
		}
		if e2e.OK() {
			return false, nil
		}
		checks = e2e.Failed
	}
	prs := landedPRs(res)
	if err := t.e.freezeLandedFailure(t.ctx, t.root, t.o.Base, prs, checks); err != nil {
		return true, err
	}
	res.Verdict = GateVerifyFailed
	res.Err = "landed train base does not pass verification: " + strings.Join(checks, ", ")
	res.Hint = "fix forward; rota round freeze names the blocked base and its repair"
	return true, nil
}
