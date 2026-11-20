package worker

import (
	"context"
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/change"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/flakeledger"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/gotestout"
	"github.com/l4ci/rota/internal/land"
	"github.com/l4ci/rota/internal/landfreeze"
	"github.com/l4ci/rota/internal/landing"
	"github.com/l4ci/rota/internal/strutil"
	"math/bits"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Merge train (#83): verify several queued PRs together once, then land them.
//
// One serial gate is the slowest step of a round (#46), and N PRs gated one at a
// time pay it N times. A train pays it once. In order:
//
//  1. CHECK each target the way `gate --check-only` does (freshness, PR
//     identity, provenance, review verdict). Missing PR sections or review verdicts
//     exclude that target; other refusals stop the train, naming that target.
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
//     member whose addition breaks the tree. Then CLASSIFY it by rerunning only
//     the failing tests on the base plus that member alone (#3095): red alone
//     makes it the culprit to send back. Green alone reruns it with each earlier
//     member, then the red prefix: a red pair or prefix is an interaction (no
//     culprit, the members named); no red at all is a flake (no culprit, the
//     tests named in a note). With LandGreen the verified prefix before it lands.

// The values of TrainOpts.OnFail.
const (
	OnFailBisect = "bisect"
	OnFailStop   = "stop"
)

// TrainOpts are the flags of `rota worker train`.
type TrainOpts struct {
	// NoWait is GateOpts.NoWait for an autopilot train.
	NoWait  bool
	Targets []string // slot names or PR refs, in merge order
	// Round reads the round lease for the train's ledger entries; Train makes
	// one when nil, so the whole train reads the lease once.
	Round *landing.RoundMemo
	Base  string
	// SyncBase is the project base branch to merge into Base before the
	// members, when Base is a milestone branch; "" for a train on the base.
	SyncBase string
	// LandGreen lands the verified prefix before the culprit when the train
	// fails verification.
	LandGreen bool
	// OnFail is what a red tier does next: OnFailBisect (the default, "") bisects
	// growing prefixes to name a culprit; OnFailStop ends the train at once with
	// the failure as evidence and no attribution.
	OnFail string
	// NoVerify lets the train run with an empty test.full (it is refused
	// otherwise); a set test.full is still run.
	NoVerify bool
	// FinalSlice names the targets that are the last slice of their
	// partial-slice issue (GateOpts.FinalSlice, per member).
	FinalSlice []string
	// Approve is the merge-approval check for the whole train: files lists the
	// union of the paths the members change.
	Approve func(files func() ([]string, error)) (*exitcode.Refusal, error)
	// VerdictRepo is GateOpts.VerdictRepo, used for every member's check.
	VerdictRepo string
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
	// Integration names the integration branch of the round
	// (integration/r<round>-<name>): the members merge into it, it is pushed
	// and verified, and nothing lands on Base. "" is a plain train.
	Integration string
	// Onto stacks the integration branch on another open one (by name): the
	// branch is built on that branch's tip and its PR targets it, until it
	// lands (#3098). "" is an integration branch on Base.
	Onto string
	// ontoBase is the base Onto's branch integrates into, set by resolveOnto.
	ontoBase string
	// RequestID is the receipt id of this call. When set, the train writes a
	// live marker under it (see TrainRun) so `rota worker train cancel` can
	// find and stop it; "" writes none.
	RequestID string
}

// cloneTrainResult gives a stage its own writable slices, including member flags.
func cloneTrainResult(r landing.TrainResult) landing.TrainResult {
	r.Skipped = slices.Clone(r.Skipped)
	r.Members = slices.Clone(r.Members)
	r.Culprits = slices.Clone(r.Culprits)
	r.Interaction = slices.Clone(r.Interaction)
	r.Verified = slices.Clone(r.Verified)
	r.Flaky = slices.Clone(r.Flaky)
	r.E2EVerified = slices.Clone(r.E2EVerified)
	r.Landed = slices.Clone(r.Landed)
	r.AlreadyMerged = slices.Clone(r.AlreadyMerged)
	r.Notes = slices.Clone(r.Notes)
	r.Order = slices.Clone(r.Order)
	r.CacheHits = slices.Clone(r.CacheHits)
	r.Transient = slices.Clone(r.Transient)
	r.Diagnostic = slices.Clone(r.Diagnostic)
	r.Excluded = slices.Clone(r.Excluded)
	r.Expired = slices.Clone(r.Expired)
	return r
}

// Train runs a merge train (see above). The base must be checked out in root.
func (e Env) Train(ctx context.Context, root string, o TrainOpts) (landing.TrainResult, error) {
	e = e.WithDefaults()
	if o.OnFail != "" && o.OnFail != OnFailBisect && o.OnFail != OnFailStop {
		return landing.TrainResult{}, fmt.Errorf("unknown on-fail policy %q: want %s or %s", o.OnFail, OnFailStop, OnFailBisect)
	}
	if o.Round == nil {
		o.Round = &landing.RoundMemo{}
	}
	if err := integrationOpts(o); err != nil {
		return landing.TrainResult{}, err
	}
	if err := refuseIntegrationPRs(root, o.Targets); err != nil {
		return landing.TrainResult{}, err
	}
	o, err := resolveOnto(root, o)
	if err != nil {
		return landing.TrainResult{}, err
	}
	var res landing.TrainResult
	if o.Plan { // a preview changes nothing, so it does not queue behind a landing
		return e.train(ctx, root, o, nil, nil)
	}
	if o.RequestID != "" {
		release, err := e.claimTrainRun(ctx, root, o)
		if err != nil && ctx.Err() == nil {
			return landing.TrainResult{}, err
		}
		if release != nil {
			defer release()
		}
	}
	lock := func(fn func(bool) error) error {
		return e.withLandingRun(ctx, root, strings.Join(o.Targets, ", "), o.NoWait, fn)
	}
	if o.Integration != "" { // it moves no base, so it does not take the land lock
		lock = func(fn func(bool) error) error {
			return e.WithIntegrationLock(ctx, root, o.Integration, func() error { return fn(false) })
		}
	}
	err = lock(func(bool) (err error) {
		cache := loadTrainCache(root)
		gated := map[string]bool{}
		res, err = e.train(ctx, root, o, cache, gated)
		book := e
		if ctx.Err() != nil && res.Verdict != landing.GatePass {
			res, err = cancelledTrain(res), nil
			book.Ctx = context.WithoutCancel(e.Ctx) // the ledger still records the cancel
		}
		trainLedger(book, o.Round, root, o.RequestID, res, gated)
		if cache.dirty {
			if serr := cache.save(); serr != nil {
				res.Notes = append(res.Notes, "TRAIN-CACHE not saved — "+serr.Error())
			}
		}
		return err
	})
	switch {
	case errors.Is(err, errLandingBusy):
		res = landing.TrainResult{Base: o.Base, Verdict: landing.GateRunning, Err: err.Error()}
		err = nil
	case err != nil && ctx.Err() != nil: // cancelled while queued for the land lock
		res, err = cancelledTrain(landing.TrainResult{Base: o.Base, Members: membersOf(o.Targets)}), nil
	}
	return res, err
}

// trainRun holds invocation dependencies only. Results travel through the
// typed plan and verification values below, never through this receiver.
type trainRun struct {
	e     Env
	ctx   context.Context
	root  string
	o     TrainOpts
	cache *trainCache
	gated map[string]bool // members whose landing ran a real gate and wrote a ledger entry
}

// trainPlan pins the refs and scratch prefixes built by planning. It is the
// complete input to verification and can be supplied without running plan.
type trainPlan struct {
	in     gateInput
	remote bool
	onCI   bool
	verify func(string) (VerifyResult, error)
	// A reduced tier replaces test.full/test.e2e for this scratch tree only.
	tierCfg          *GateConfig
	tierRan          string
	baseRef, baseSHA string
	// syncSHA is the optional milestone sync commit, before any member merges.
	syncSHA string
	headRef func(landing.TrainMember) string
	heads   []string // pinned member heads, in merge order
	tips    []string // tips[i] has the first i members merged
	scratch string
	cleanup func()
	// integration is the integration branch's record (nil for a plain train)
	// and intTip the tip pushed to it: the members plus the base merge.
	integration *landing.Integration
	intTip      string
}

// phase reports the stage the train is in on o.Say, one PHASE line per change
// (#2753), so an operator watching a long train sees where it is.
// memberCheckOpts is the read-only gate check a train member passes before it
// is built, the one collector of the member's record gaps.
func (t *trainRun) memberCheckOpts(target string, in gateInput) GateOpts {
	o := t.o
	return GateOpts{Slot: target, Base: o.Base, PRBase: o.ontoBase, CheckOnly: true, FinalSlice: slices.Contains(o.FinalSlice, target), VerdictRepo: o.VerdictRepo, CheckReviews: o.CheckReviews, in: &in}
}

func (t *trainRun) phase(format string, args ...any) {
	if t.o.Say != nil {
		t.o.Say("PHASE " + fmt.Sprintf(format, args...))
	}
}

// tipAt is the short SHA of the tree with k members merged; "" when the plan
// carries no such tip.
func tipAt(tips []string, k int) string {
	if k < 0 || k >= len(tips) {
		return ""
	}
	return strutil.ShortSHA(tips[k])
}

// failureOf describes one failed verify run of the tree with members merged.
func failureOf(tier string, vr VerifyResult, members int, tip string) landing.TrainFailure {
	f := landing.TrainFailure{Tier: tier, Command: strings.Join(vr.Failed, "; "), Members: members, Tip: tip}
	f.Tests, _ = gotestout.FailingTests(vr.Log)
	return f
}

func (p trainPlan) rem() string { return p.in.cfg.RemoteName() }
func (p trainPlan) verifyCfg() GateConfig {
	if p.tierCfg != nil {
		return *p.tierCfg
	}
	return p.in.cfg
}

// trainPlanning owns mutable scratch state only while plan runs. Only its
// trainPlan and public answer leave that operation.
type trainPlanning struct {
	trainPlan
	res landing.TrainResult
}

// trainVerified is the prefix verification permits to land. The plan and
// public answer are separate arguments, rather than mutable fields in it.
type trainVerified struct{ passing int }

func (e Env) train(ctx context.Context, root string, o TrainOpts, cache *trainCache, gated map[string]bool) (landing.TrainResult, error) {
	t := &trainRun{e: e, ctx: ctx, root: root, o: o, cache: cache, gated: gated}
	start := landingStageResult[struct{}, landing.TrainResult]{Result: landing.TrainResult{Base: o.Base}}
	planned := runLandingStage(start, t.plan)
	defer func() {
		if planned.Value.cleanup != nil {
			planned.Value.cleanup()
		}
	}()
	verified := runLandingStage(planned, func(p trainPlan) (trainVerified, landing.TrainResult, bool, error) {
		return t.verifyStage(p, planned.Result)
	})
	checked := runLandingStage(verified, func(prefix trainVerified) (struct{}, landing.TrainResult, bool, error) {
		return t.vacuousStage(planned.Value, verified.Result, prefix)
	})
	behaved := runLandingStage(checked, func(struct{}) (trainVerified, landing.TrainResult, bool, error) {
		return t.behaviourStage(planned.Value, checked.Result, verified.Value)
	})
	landed := runLandingStage(behaved, func(prefix trainVerified) (struct{}, landing.TrainResult, bool, error) {
		land := t.land
		if planned.Value.integration != nil {
			land = t.integrationLand
		}
		res, err := land(planned.Value, behaved.Result, prefix)
		return struct{}{}, res, true, err
	})
	res := landed.Result
	if it := planned.Value.integration; it != nil && res.Verdict == landing.GateInteraction {
		res.Hint = t.integrationInteractionHint(*it, res.Interaction)
	}
	return res, landed.Err
}

// trainBrokeResult preserves the public CHECK-BROKE answer without receiver state.
func trainBrokeResult(res *landing.TrainResult, msg string) (bool, error) {
	res.Verdict, res.Err = landing.GateCheckBroke, checkBrokeMessage("train", msg)
	return true, nil
}

// plan is stages 1 to 3: check every member, order them, run the pre-merge
// policy and merge them in a scratch worktree. The caller releases the
// returned scratch tree on every exit.
func (t *trainRun) plan(_ struct{}) (out trainPlan, result landing.TrainResult, stopped bool, stageErr error) {
	p := trainPlanning{res: landing.TrainResult{Base: t.o.Base}}
	t.phase("plan: %d target(s) onto %s", len(t.o.Targets), t.o.Base)
	// Until a normal return transfers cleanup in out, planning still owns it.
	// This also releases the scratch tree on a panic before the handoff exists.
	defer func() {
		if out.cleanup == nil && p.cleanup != nil {
			p.cleanup()
		}
	}()

	e, ctx, root, o, res := t.e, t.ctx, t.root, t.o, &p.res
	if len(o.Targets) == 0 {
		return p.trainPlan, p.res, true, fail(exitcode.ExitUsage, "a train needs at least one PR or slot")
	}
	reg, err := LoadRegistry(root)
	if err != nil {
		return p.trainPlan, p.res, true, err
	}
	if !reg.Exists {
		return p.trainPlan, p.res, true, fail(exitcode.ExitResolution, "no worker pool — run rota worker pool init first")
	}
	// A stacked integration's base is another integration branch, which lives on
	// the remote only: the project's base stays checked out, and the fetch
	// before the merge reads the branch from the remote.
	if o.Onto == "" {
		if _, code := e.git(root, "rev-parse", "--verify", "--quiet", o.Base); code != 0 {
			return p.trainPlan, p.res, true, fail(exitcode.ExitResolution, fmt.Sprintf("base branch '%s' does not exist", o.Base))
		}
		if cur, _ := e.git(root, "rev-parse", "--abbrev-ref", "HEAD"); cur != o.Base {
			return p.trainPlan, p.res, true, fail(exitcode.ExitResolution, fmt.Sprintf("a train must run with %s checked out (currently on %s)", o.Base, cur))
		}
	}
	// A bad test.fullWhere, test.ciChecks or ledger is refused before any
	// member is checked, and the train carries this read through.
	in, err := e.GateInput(root)
	if err != nil {
		return p.trainPlan, p.res, true, err
	}
	in.cfg.WarnLegacy(e.Stderr)
	if err := in.cfg.RemoteErr; err != nil {
		return p.trainPlan, p.res, true, fail(exitcode.ExitRefused, err.Error())
	}
	p.in = in
	seen := map[string]bool{}
	withPR := 0
	for _, tg := range o.Targets {
		gt, err := reg.GateTargetAny(tg)
		if err != nil {
			return p.trainPlan, p.res, true, err
		}
		key := gt.Branch + "|" + gt.PR
		if gt.PR != "" {
			// External targets keep the caller's spelling until the gate reads
			// the forge: 7, #7 and its URL still name the same train member.
			key = "pr|" + prNumText(gt.PR)
		}
		if seen[key] {
			return p.trainPlan, p.res, true, fail(exitcode.ExitUsage, fmt.Sprintf("%s names a PR already in the train", tg))
		}
		seen[key] = true
		if gt.PR != "" {
			withPR++
		}
	}
	if o.Integration != "" {
		if p.integration, err = integrationPlan(reg, o, withPR, len(o.Targets)); err != nil {
			return p.trainPlan, p.res, true, err
		}
	}
	if withPR != 0 && withPR != len(o.Targets) {
		return p.trainPlan, p.res, true, fail(exitcode.ExitUsage, fmt.Sprintf("a train is all PRs or all slots without one, not a mix: PRs merge onto %s/%s and slots onto the local %s", p.rem(), o.Base, o.Base))
	}

	// Freeze is a queue-wide refusal, before static sections can prune members.
	frozen, err := landfreeze.Load(ctx, e.Git, root)
	if err != nil {
		res.Verdict, res.Err, res.Hint = landing.GateFreeze, landfreeze.UnreadableLine(err), landfreeze.ClearHint
		return p.trainPlan, p.res, true, nil
	}
	if frozen.Frozen() {
		if !frozen.Binds(o.Base) {
			res.Verdict, res.Err, res.Hint = landing.GateFreeze, frozen.Line(), "repairs must target the frozen base"
			return p.trainPlan, p.res, true, nil
		}
		for _, tg := range o.Targets {
			gt, err := reg.GateTargetAny(tg)
			if err != nil {
				return p.trainPlan, p.res, true, err
			}
			if frozen.Allows(gt.Issue, gt.PR) {
				continue
			}
			// A PR outside the registry derives its repair issue from its head branch.
			if gt.External {
				gr, err := e.Gate(ctx, root, GateOpts{Slot: tg, Base: o.Base, PRBase: o.ontoBase, CheckOnly: true, NoVerdict: true, in: &in})
				if err != nil {
					return p.trainPlan, p.res, true, err
				}
				if gr.Verdict != landing.GateFreeze {
					continue
				}
			}
			res.Verdict, res.Err, res.Hint = landing.GateFreeze, frozen.Line(), "designate the repair with rota round freeze before landing or assigning"
			return p.trainPlan, p.res, true, nil
		}
	}

	// 1. Pre-filter static PR sections, then check the remaining members.
	targets, done, err := t.filterPRSections(res, in, reg, o.Targets)
	if done || err != nil {
		return p.trainPlan, p.res, true, err
	}
	var merged []landing.TrainMember
	for _, tg := range targets {
		gr, err := e.Gate(ctx, root, t.memberCheckOpts(tg, in))
		if err != nil {
			if gr.Verdict == landing.GateVerdictBlocked {
				res.Verdict, res.Culprit = gr.Verdict, tg
			}
			return p.trainPlan, p.res, true, err
		}
		res.Notes = appendNew(res.Notes, gr.Notes...)
		if gr.Verdict == landing.GateAlreadyMerged {
			merged = append(merged, landing.TrainMember{Target: tg, Branch: gr.Branch, PR: gr.PR})
			res.AlreadyMerged = append(res.AlreadyMerged, tg)
			res.Notes = append(res.Notes, gr.Err)
			continue
		}
		if excludeTrainMember(res, tg, gr) {
			continue
		}
		if !gr.OK() {
			res.Verdict, res.Culprit, res.CulpritGate, res.Err, res.Hint = gr.Verdict, tg, gr, gr.Err, gr.Hint
			return p.trainPlan, p.res, true, nil
		}
		res.Members = append(res.Members, landing.TrainMember{Target: tg, Branch: gr.Branch, PR: gr.PR, Tier: gr.Tier, CheckedHead: gr.CheckedHead, ResolvedIssue: gr.Issue})
		p.remote = p.remote || gr.PR != ""
	}
	if err := filterSectionOrder(*res, &o, merged); err != nil {
		return p.trainPlan, p.res, true, err
	}
	if len(res.Members) == 0 {
		if len(res.Culprits) == 0 {
			res.Verdict = landing.GatePass
			if o.Plan {
				res.Verdict, res.Plan = landing.GatePlan, &landing.TrainPlan{}
			}
			return p.trainPlan, p.res, true, nil
		}
		c := res.Culprits[0]
		res.Verdict, res.Culprit, res.CulpritGate, res.Err, res.Hint = c.Gate.Verdict, c.Target, c.Gate, c.Gate.Err, c.Gate.Hint
		return p.trainPlan, p.res, true, nil
	}
	p.baseRef = o.Base
	if p.remote {
		p.baseRef = p.rem() + "/" + o.Base
	}
	p.headRef = func(m landing.TrainMember) string {
		if m.PR != "" {
			return p.rem() + "/" + m.Branch
		}
		return m.Branch
	}
	baseRef, headRef := p.baseRef, p.headRef
	if o.Plan {
		done, err := t.preview(&p)
		return p.trainPlan, p.res, done, err
	}
	verify, onCI, brokeMsg, err := e.fullTier(ctx, root, "train", in)
	if err != nil {
		return p.trainPlan, p.res, true, err
	}
	if brokeMsg != "" {
		done, err := trainBrokeResult(res, brokeMsg)
		return p.trainPlan, p.res, done, err
	}
	p.verify, p.onCI = verify, onCI

	// Order the members to minimise predicted conflicts, or take --order.
	foots, err := e.trainFootprints(root, baseRef, res.Members, headRef)
	if err != nil {
		done, err := trainBrokeResult(res, err.Error())
		return p.trainPlan, p.res, done, err
	}
	perm, lines, nIso, err := orderTrain(res.Members, foots, o.Order)
	if err != nil {
		return p.trainPlan, p.res, true, err
	}
	autoOrder := len(o.Order) == 0
	ms := make([]landing.TrainMember, len(perm))
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
		res.Verdict = landing.GateApprovalRequired
		res.Refusal = ref
		if ref != nil {
			perr = ref.Err
		}
		return p.trainPlan, p.res, true, perr
	case perr != nil:
		done, err := trainBrokeResult(res, perr.Error())
		return p.trainPlan, p.res, done, err
	case r != nil:
		res.Verdict, res.Err, res.Hint, res.Expired = r.Verdict, r.Err, r.Hint, r.Expired
		return p.trainPlan, p.res, true, nil
	}

	// 3. Merge in order in a scratch worktree.
	if (o.SyncBase != "" || p.integration != nil) && p.remote {
		if _, code := e.git(root, "fetch", p.rem(), "-q"); code != 0 {
			done, err := trainBrokeResult(res, "git fetch "+p.rem()+" failed before merging the members")
			return p.trainPlan, p.res, done, err
		}
	}
	baseSHA, code := e.git(root, "rev-parse", baseRef)
	if code != 0 {
		done, err := trainBrokeResult(res, fmt.Sprintf("git rev-parse %s exited %d", baseRef, code))
		return p.trainPlan, p.res, done, err
	}
	p.baseSHA = baseSHA
	p.heads = make([]string, len(res.Members))
	for i, m := range res.Members {
		if p.heads[i], code = e.git(root, "rev-parse", headRef(m)); code != 0 {
			done, err := trainBrokeResult(res, fmt.Sprintf("git rev-parse %s exited %d", headRef(m), code))
			return p.trainPlan, p.res, done, err
		}
		// An integration branch trusts rota's member merges without a review of
		// their own, so it merges the head the member's review was checked at:
		// one pushed since is merged by a rerun, after its own review.
		if p.integration != nil && m.CheckedHead != "" && p.heads[i] != m.CheckedHead {
			res.Notes = append(res.Notes, fmt.Sprintf("PINNED %s — %s moved to %s after its check; merging the checked %s", m.Target, m.Branch, strutil.ShortSHA(p.heads[i]), strutil.ShortSHA(m.CheckedHead)))
			p.heads[i] = m.CheckedHead
		}
	}
	p = t.pickTier(p)
	res = &p.res
	t.cache.retainBase(baseSHA)
	start := baseSHA
	if p.integration != nil {
		var done bool
		if start, done, err = t.integrationStart(&p); done || err != nil {
			return p.trainPlan, p.res, true, err
		}
		t.skipIntegrated(&p, start)
	}
	scratch, cleanup, err := e.scratchTree(ctx, root, start, "rota-train-")
	if err != nil {
		done, err := trainBrokeResult(res, err.Error())
		return p.trainPlan, p.res, done, err
	}
	p.scratch, p.cleanup = scratch, cleanup
	if o.SyncBase != "" {
		if done, err := t.syncBase(&p); done || err != nil {
			return p.trainPlan, p.res, done, err
		}
	}
	t.phase("merge: %d member(s) onto %s %s", len(res.Members), o.Base, strutil.ShortSHA(baseSHA))
	tips, failed, merr, brokeMsg := e.mergeTrain(ctx, scratch, o.Base, res.Members, p.heads)
	p.tips = tips
	if brokeMsg != "" {
		done, err := trainBrokeResult(res, brokeMsg)
		return p.trainPlan, p.res, done, err
	}
	var mf land.Failure
	if merr != nil {
		mf = land.Classify(merr)
	}
	if merr != nil && p.integration == nil && autoOrder && failed >= nIso && failed > 0 && mf.Kind == land.FailConflict {
		// A shared-path member conflicts with the ones before it. Git's pairwise
		// conflicts do not depend on merge order, so no other order is tried.
		res.Verdict, res.Culprit = landing.GateOrder, res.Members[failed].Target
		res.Members[failed].Culprit = true
		res.Err = fmt.Sprintf("TRAIN-BLOCKED order — %s; the shared-path members conflict in the order tried (%s); nothing pushed", conflictPair(res.Members, foots, failed), orderText(res.Members))
		res.Hint = "rebase one of the pair on the other's merge, or land them in separate trains, or pass --order to try another"
		return p.trainPlan, p.res, true, nil
	}
	if merr != nil && p.integration != nil && mf.Kind == land.FailConflict {
		done, err := t.integrationConflict(&p, failed, mf.Paths)
		return p.trainPlan, p.res, done, err
	}
	if merr != nil {
		m := res.Members[failed]
		res.Culprit = m.Target
		res.Members[failed].Culprit = true
		res.Verdict = landing.GateMergeFailed
		res.Err, res.Hint = mf.Refusal(land.Words{
			Failed:       fmt.Sprintf("TRAIN-FAIL %s — merging %s into the scratch tree failed", m.Target, m.Branch),
			Conflict:     fmt.Sprintf("TRAIN-FAIL %s — %s does not merge onto %s with the %d member(s) before it: conflict", m.Target, m.Branch, o.Base, failed),
			ConflictHint: fmt.Sprintf("send %s back to merge %s, or run the train without it", m.Target, o.Base),
		})
		return p.trainPlan, p.res, true, nil
	}
	if p.integration != nil {
		done, err := t.assembleIntegration(&p)
		return p.trainPlan, p.res, done, err
	}
	return p.trainPlan, p.res, false, nil
}

// syncBase merges the project base into the milestone Base in the scratch tree,
// before the members, so the verify runs on base + milestone + members. A
// conflict ends the train with merge-failed naming the milestone and the
// paths; nothing has been pushed at this point.
func (t *trainRun) syncBase(p *trainPlanning) (bool, error) {
	e, ctx, o, res := t.e, t.ctx, t.o, &p.res
	from := o.SyncBase
	if p.remote {
		from = p.rem() + "/" + o.SyncBase
	}
	sha, code := e.git(t.root, "rev-parse", from)
	if code != 0 {
		return trainBrokeResult(res, fmt.Sprintf("git rev-parse %s exited %d", from, code))
	}
	run := func(args ...string) (git.Result, error) { return e.Git(ctx, p.scratch, args...) }
	tip, err := land.LandLocal(land.LocalPorts{Git: run, Recover: land.RecoveryGit(ctx, e.Git, p.scratch)},
		land.LocalRequest{Head: sha, Base: "scratch tree", Message: land.SyncSubject(o.SyncBase, o.Base)})
	if err != nil {
		if errors.As(err, new(*land.PostMergeError)) {
			return trainBrokeResult(res, err.Error())
		}
		res.Verdict = landing.GateMergeFailed
		res.Err, res.Hint = land.Classify(err).Refusal(land.Words{
			Failed:       fmt.Sprintf("TRAIN-FAIL sync — merging %s into %s in the scratch tree failed", o.SyncBase, o.Base),
			Conflict:     fmt.Sprintf("TRAIN-FAIL sync — merging %s into %s conflicts in: {paths}; nothing pushed", o.SyncBase, o.Base),
			ConflictHint: fmt.Sprintf("merge %s into %s by hand, or file a fix issue tagged to the milestone, then re-run the train", o.SyncBase, o.Base),
		})
		return true, nil
	}
	if tip != p.baseSHA {
		p.syncSHA = tip
		t.cache.retainBase(tip)
	}
	return false, nil
}

// pushSync lands the verified sync merge on the milestone Base ahead of the
// first member: a fast-forward push to origin for a PR train, a ff-only merge
// of the checked-out Base for a slot train.
func (t *trainRun) pushSync(p trainPlan) error {
	e, root, o := t.e, t.root, t.o
	if p.remote {
		if out, code := e.runGit(t.ctx, root, "push", "-q", p.rem(), p.syncSHA+":refs/heads/"+o.Base); code != 0 {
			return fmt.Errorf("git push of the sync merge to %s/%s failed: %s", p.rem(), o.Base, strings.TrimSpace(out))
		}
		if _, code := e.git(root, "fetch", p.rem(), "-q"); code != 0 {
			return errors.New("git fetch " + p.rem() + " failed after pushing the sync merge")
		}
		return nil
	}
	if out, code := e.runGit(t.ctx, root, "merge", "--ff-only", "-q", p.syncSHA); code != 0 {
		return fmt.Errorf("fast-forwarding %s to the sync merge failed: %s", o.Base, strings.TrimSpace(out))
	}
	return nil
}

// preview answers --plan: the pairs of members that really conflict, the
// largest conflict-free subset in merge order and the members left out. It runs
// no tests and touches no ref. Only pairs sharing a path are merge-tried, with
// git merge-tree; three-way interactions between members are not looked for.
func (t *trainRun) preview(p *trainPlanning) (bool, error) {
	e, root, res := t.e, t.root, &p.res
	foots, err := e.trainFootprints(root, p.baseRef, res.Members, p.headRef)
	if err != nil {
		return trainBrokeResult(res, err.Error())
	}
	n := len(res.Members)
	clash := make([][]bool, n)
	for i := range clash {
		clash[i] = make([]bool, n)
	}
	plan := &landing.TrainPlan{}
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			both := pairwisePaths(foots[i].paths, foots[j].paths, nil)
			if len(both) == 0 {
				continue
			}
			conflict, _, code := pairwiseMergeConflict(e.git, root, p.headRef(res.Members[i]), p.headRef(res.Members[j]))
			if code != 0 {
				return trainBrokeResult(res, fmt.Sprintf("git merge-tree of %s and %s exited %d", memberLabel(res.Members[i]), memberLabel(res.Members[j]), code))
			}
			if conflict {
				clash[i][j], clash[j][i] = true, true
				plan.Conflicts = append(plan.Conflicts, landing.TrainConflict{A: res.Members[i].Target, B: res.Members[j].Target, Paths: both})
			}
		}
	}
	keep := largestFree(clash)
	var sub []landing.TrainMember
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
		b := landing.TrainBlocked{Target: m.Target}
		for _, k := range keep {
			if clash[i][k] {
				b.By = append(b.By, res.Members[k].Target)
			}
		}
		plan.Blocked = append(plan.Blocked, b)
	}
	if len(plan.Landable) > 0 {
		plan.Command = fmt.Sprintf("rota worker train --base %s --order %s %s", t.o.Base, strings.Join(plan.Landable, ","), strings.Join(plan.Landable, " "))
		if plan.NoVerify = noVerifyRule(p.in.cfg.where, p.in.cfg.Full, p.in.cfg.E2E); plan.NoVerify {
			plan.Command += " --no-verify"
		}
	}
	res.Plan, res.Verdict = plan, landing.GatePlan
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
func (t *trainRun) pickTier(p trainPlanning) trainPlanning {
	res := &p.res
	tier := change.TierDocs
	if len(res.Members) == 0 || t.o.SyncBase != "" {
		tier = change.TierFull
	}
	policy, perr := p.in.cfg.ChangePolicy()
	var files []string
	lines := 0
	seen := map[string]bool{}
	for i, m := range res.Members {
		tier = change.Higher(tier, change.Tier(m.Tier))
		if tier == change.TierFull {
			break
		}
		span := p.baseSHA + "..." + p.heads[i]
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
	if tier == change.TierFull || p.onCI {
		return p
	}
	cfg, ran, skipped := p.in.cfg.forTier(tier, files)
	if len(skipped) == 0 {
		return p
	}
	p.tierCfg, p.tierRan, res.Skipped = &cfg, ran, skipped
	ctx, e, ledger := t.ctx, t.e, p.in.ledger
	p.verify = func(dir string) (VerifyResult, error) { return e.runGateVerify(ctx, cfg.Full, dir, ledger) }
	res.Notes = append(res.Notes, fmt.Sprintf("GATE-TIER train — %s (every member is %s or less); ran %s; skipped %s", tier, tier, ran, strings.Join(skipped, ", ")))
	return p
}

// verifyStage is stage 4: test.full, then test.e2e on the same tree, with a
// bisect on a red tier. It returns the passing prefix. The tier policy (what
// a result means, the note text) is tierOutcomeOf's; this stage owns the cache
// and the bisect.
func (t *trainRun) verifyStage(p trainPlan, result landing.TrainResult) (trainVerified, landing.TrainResult, bool, error) {
	result = cloneTrainResult(result)
	v := trainVerified{passing: len(result.Members)}

	e, ctx, o, res, scratch := t.e, t.ctx, t.o, &result, p.scratch
	baseSHA := p.baseSHA
	if p.syncSHA != "" { // the verified tree includes the sync merge
		baseSHA = p.syncSHA
	}
	n := len(res.Members)
	v.passing = n
	// A local run that cannot start is an error, as before; a CI push or
	// forge read that fails is CHECK-BROKE, since nothing has landed.
	verifyErr := func(err error) (bool, error) {
		if p.onCI {
			return trainBrokeResult(res, err.Error())
		}
		return true, err
	}
	vr := &verifyRun{
		res: res, cache: t.cache, ctx: ctx, baseSHA: baseSHA, heads: p.heads, tips: p.tips, n: n,
		phase: t.phase, passing: n, onFailStop: o.OnFail == OnFailStop,
		record: func(tier string, prs []string, r VerifyResult) error {
			e.recordVerification(t.root, o.Round, tier, GateTarget{}, prs, r)
			return e.recordFlakes(t.root, o.Round, prs, r)
		},
		stop:  func(tier string, failed VerifyResult) { t.stopTrain(res, p, tier, baseSHA, failed) },
		rerun: func(tier string, failed VerifyResult) (bool, error) { return t.rerunRed(p, res, tier, failed) },
		bisectTrain: func(tier string, run func(int) (VerifyResult, error), failed VerifyResult) (int, bool, error) {
			classify := func(k int, failed VerifyResult) (redClass, error) { return t.classifyRed(p, res, k, failed) }
			return e.bisectTrain(scratch, res, p.tips, tier, run, classify, failed, o.LandGreen)
		},
	}
	if p.integration != nil {
		vr.tipOf = p.tipOf
	}
	fullTierName := tierFull
	if p.tierRan != "" {
		fullTierName = p.tierRan // a cached test.full result never answers a test.docs run
	}
	if p.onCI {
		fullTierName = tierFull + "@ci"
	}
	full := vr.cached(fullTierName, func() (VerifyResult, error) { return p.verify(scratch) })
	e2e := vr.cached(tierE2E, func() (VerifyResult, error) { return e.runE2E(ctx, p.in.cfg.E2E, scratch, p.in.ledger) })
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
		stop: func(r VerifyResult) bool {
			rr, done := trainStopped(*res, r)
			if done {
				result = rr
			}
			return done
		},
		red: func(tier string, r VerifyResult) (bool, error) {
			if tier == tierFull {
				return vr.bisect(tierFull, full, r, fullTierName)
			}
			return vr.bisect(tierE2E, e2e, r, tierE2E)
		},
		notes: &res.Notes, verified: &res.Verified, e2eVerified: &res.E2EVerified, excluded: &res.Excluded, flaky: &res.Flaky,
	}
	// --no-verify on an integration train builds and pushes the branch and runs
	// no tier, so the order build, look, refs, verify needs no cancel (#3683).
	// Nothing is recorded as verified: the gate verifies the tip itself.
	if p.integration != nil && o.NoVerify {
		res.Notes = append(res.Notes, fmt.Sprintf("NO-VERIFY %s — verify skipped (--no-verify); the gate verifies the final tip before it lands", p.integration.Branch))
		return v, result, false, nil
	}
	ended, err := w.walk(p.verifyCfg(), tierFull)
	if err != nil {
		if w.at == tierFull {
			done, err := verifyErr(err)
			return v, result, done, err
		}
		return v, result, true, err
	}
	if ended {
		return v, result, true, nil
	}
	green := !w.fullRed

	// A member earlier named culprit that this verified train carries was flaky:
	// the failure did not reproduce on a different combination.
	if vr.passing == n && !w.fullEmpty {
		vr.forgive()
	}

	if !green && vr.passing > 0 && len(p.verifyCfg().E2E) > 0 {
		res.Notes = append(res.Notes, noE2ENote(vr.passing))
	}
	v.passing = vr.passing
	return v, result, false, nil
}

// behaviourStage runs each member's `## Try it` steps (#915), after test.full
// and test.e2e passed and before anything lands. A member's steps run in the
// scratch tree with the members up to it merged, through a CheckOnly gate, so
// the collaborator trust check, the scrubbed environment, the transcript, the
// behaviour verdict and the PR comment are the gate's own. The first failing
// member is the culprit: nothing lands, or under LandGreen the members before
// it do. It lowers the returned passing prefix; it is a no-op without ItemBody.
func (t *trainRun) behaviourStage(p trainPlan, result landing.TrainResult, v trainVerified) (trainVerified, landing.TrainResult, bool, error) {
	result = cloneTrainResult(result)

	e, ctx, root, o, res := t.e, t.ctx, t.root, t.o, &result
	if o.ItemBody == nil {
		return v, result, false, nil
	}
	for i := 0; i < v.passing; i++ {
		if ctx.Err() != nil { // cancelled: run no further member's steps
			return v, result, true, nil
		}
		m := res.Members[i]
		// Files an earlier member's steps wrote must not reach this member's run.
		if _, code := e.git(p.scratch, "clean", "-q", "-fdx"); code != 0 {
			done, err := trainBrokeResult(res, "git clean failed in the scratch tree")
			return v, result, done, err
		}
		if _, code := e.git(p.scratch, "checkout", "-q", "-f", "--detach", p.tips[i+1]); code != 0 {
			done, err := trainBrokeResult(res, "git checkout "+p.tips[i+1]+" failed in the scratch tree")
			return v, result, done, err
		}
		gr, err := e.Gate(ctx, root, GateOpts{Slot: m.Target, Base: o.Base, PRBase: o.ontoBase, CheckOnly: true, FinalSlice: slices.Contains(o.FinalSlice, m.Target), VerdictRepo: o.VerdictRepo, CheckReviews: o.CheckReviews,
			ItemBody: o.ItemBody, BehaviourDir: p.scratch, in: &p.in})
		if ctx.Err() != nil { // a step the cancel stopped is no finding
			return v, result, true, nil
		}
		if err != nil {
			return v, result, true, err
		}
		res.Notes = appendNew(res.Notes, gr.Notes...)
		// The gate read the head again; its verdict and comment belong to the
		// head the steps ran on, so a head that moved since plan ends the train.
		if cur, _ := e.git(root, "rev-parse", p.headRef(m)); cur != p.heads[i] {
			result, _ = e.trainMoved(*res, m.Target, fmt.Sprintf("%s moved from %s to %s while the train ran its Try it steps", p.headRef(m), strutil.ShortSHA(p.heads[i]), strutil.ShortSHA(cur)))
			return v, result, true, nil
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
			return v, result, true, nil
		}
		res.Hint = strings.TrimSpace(fmt.Sprintf("%s; the verified first %d member(s) landed (--land-green)", res.Hint, i))
		v.passing = i
		return v, result, false, nil
	}
	return v, result, false, nil
}

// land is stage 5. The tree that was verified must still be the one that lands.
func (t *trainRun) land(p trainPlan, result landing.TrainResult, prefix trainVerified) (landing.TrainResult, error) {
	e, ctx, root, o, tips, heads := t.e, t.ctx, t.root, t.o, p.tips, p.heads
	res, baseRef, baseSHA, headRef, passing := cloneTrainResult(result), p.baseRef, p.baseSHA, p.headRef, prefix.passing
	n := len(res.Members)
	if p.remote {
		if _, code := e.git(root, "fetch", p.rem(), "-q"); code != 0 {
			return e.trainBroke(res, "git fetch "+p.rem()+" failed before landing")
		}
	}
	verdict, culprit := res.Verdict, res.Culprit
	t.phase("land: %d member(s), tip %s", passing, tipAt(tips, passing))
	if cur, _ := e.git(root, "rev-parse", baseRef); cur != baseSHA {
		return e.trainMoved(res, "", fmt.Sprintf("%s moved from %s to %s while the train verified", baseRef, strutil.ShortSHA(baseSHA), strutil.ShortSHA(cur)))
	}
	for i := 0; i < passing; i++ {
		if cur, _ := e.git(root, "rev-parse", headRef(res.Members[i])); cur != heads[i] {
			return e.trainMoved(res, res.Members[i].Target, fmt.Sprintf("%s moved from %s to %s while the train verified", headRef(res.Members[i]), strutil.ShortSHA(heads[i]), strutil.ShortSHA(cur)))
		}
	}
	if passing > 0 && p.syncSHA != "" {
		if err := t.pushSync(p); err != nil {
			return e.trainMoved(res, "", err.Error())
		}
	}
	for i := 0; i < passing; i++ {
		if ctx.Err() != nil { // cancelled: land no further member
			return res, nil
		}
		m := res.Members[i]
		if i > 0 { // each landing must start from exactly the tree the scratch merge had at that point
			if p.remote {
				if _, code := e.git(root, "fetch", p.rem(), "-q"); code != 0 {
					return e.trainBroke(res, "git fetch "+p.rem()+" failed between landings")
				}
			}
			got, _ := e.git(root, "rev-parse", baseRef+"^{tree}")
			if want, _ := e.git(root, "rev-parse", tips[i]+"^{tree}"); got != want {
				return e.trainMoved(res, m.Target, fmt.Sprintf("%s changed outside the train after %d landing(s) (tree %s, verified %s)", baseRef, i, strutil.ShortSHA(got), strutil.ShortSHA(want)))
			}
		}
		t.gated[m.Target] = true
		// A landing that started runs to its end: a cancel mid-push would leave
		// the member's state unknown, so it takes effect before the next member.
		shielded := e
		shielded.Ctx = context.WithoutCancel(e.Ctx)
		gr, err := shielded.Gate(context.WithoutCancel(ctx), root, GateOpts{Slot: m.Target, Base: o.Base, LandVerified: true, NoVerdict: true, FinalSlice: slices.Contains(o.FinalSlice, m.Target), HoldsLandLock: true, Round: o.Round, in: &p.in})
		res.Notes = appendNew(res.Notes, gr.Notes...)
		res.Notes = appendNew(res.Notes, gr.Warnings...)
		res.Changed = res.Changed || gr.Changed
		if gr.Changed && gr.Verdict != landing.GatePass { // the PR is on the base but the gate could not finish (merged-remotely)
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
			if stop, err := t.verifyLanded(p, &res); stop || err != nil {
				return res, err
			}
			return e.trainMoved(res, "", fmt.Sprintf("the landed tree %s differs from the verified scratch tree %s; the forge merged differently than git did", strutil.ShortSHA(tree), strutil.ShortSHA(want)))
		}
	}
	res.Verdict, res.Culprit = verdict, culprit
	if verdict == "" {
		res.Verdict = landing.GatePass
	}
	if res.Verdict == landing.GatePass && passing == len(res.Members) && passing > 0 && len(res.Verified) > 0 {
		if err := e.clearVerifiedFreeze(ctx, root, o.Base); err != nil {
			return res, err
		}
	}
	return res, nil
}

// mergeTrain merges heads in order onto the scratch tree. tips[i] is the tree
// with the first i members merged. A failed merge returns its error and the
// member's position; brokeMsg is set when git itself failed.
func (e Env) mergeTrain(ctx context.Context, scratch, base string, ms []landing.TrainMember, heads []string) (tips []string, failed int, merr error, brokeMsg string) {
	tips = make([]string, len(ms)+1)
	tips[0], _ = e.git(scratch, "rev-parse", "HEAD")
	for i, m := range ms {
		run := func(args ...string) (git.Result, error) { return e.Git(ctx, scratch, args...) }
		tip, err := land.LandLocal(land.LocalPorts{Git: run, Recover: land.RecoveryGit(ctx, e.Git, scratch)},
			land.LocalRequest{Head: heads[i], Base: "scratch tree", Message: land.TrainSubject(m.Branch, base)})
		if err != nil {
			if errors.As(err, new(*land.PostMergeError)) {
				return tips, 0, nil, err.Error()
			}
			return tips, i, err, ""
		}
		tips[i+1] = tip
	}
	return tips, 0, nil, ""
}

func (e Env) trainBroke(res landing.TrainResult, msg string) (landing.TrainResult, error) {
	res.Verdict, res.Err = landing.GateCheckBroke, checkBrokeMessage("train", msg)
	return res, nil
}

// trainStopped ends the train when a CI run gave no answer (ci-not-run,
// verify-timeout). Nothing has landed at any point it is called.
func trainStopped(res landing.TrainResult, vr VerifyResult) (landing.TrainResult, bool) {
	v, msg, hint := vr.stopVerdict("train")
	if v == "" {
		return res, false
	}
	res.Verdict, res.Err, res.Hint = v, msg, hint
	return res, true
}

func (e Env) trainMoved(res landing.TrainResult, culprit, msg string) (landing.TrainResult, error) {
	res.Verdict, res.Culprit = landing.GateBaseMoved, culprit
	if len(res.Landed) == 0 {
		res.Err = "BASE-MOVED train — " + msg + "; nothing landed"
		res.Hint = "re-run the train on the new base"
		return res, nil
	}
	res.Err = fmt.Sprintf("BASE-MOVED train — %s; landed %d of %d member(s): %s", msg, len(res.Landed), len(res.Members), strings.Join(res.Landed, ", "))
	res.Hint = fmt.Sprintf("landed %d of %d member(s); re-run the train for the rest on the new base", len(res.Landed), len(res.Members))
	return res, nil
}

// failedSummary names what a red tier failed: its failing tests when the log
// shows them, else the failed commands.
func failedSummary(failed VerifyResult) string {
	if tests, _ := gotestout.FailingTests(failed.Log); len(tests) > 0 {
		return strings.Join(tests, ", ")
	}
	return strings.Join(failed.Failed, "; ")
}

// diagnoseEstimate is the line printed before a bisect starts: how many runs
// the diagnosis needs for an n-member train and what they cost at the last
// verify's duration. A bisect takes ceil(log2 n) prefix runs plus the base
// check; the red classify reruns only the failing tests, at most a few runs.
func diagnoseEstimate(n int, verify time.Duration) string {
	prefix := bits.Len(uint(max(n-1, 0)))
	if n > 1 {
		prefix++ // the base check when no prefix is green
	}
	runs := prefix + 3 // classify: alone, pairs, red prefix
	return fmt.Sprintf("diagnose estimate: up to %d run(s) (%d prefix + up to 3 classify), last verify took %s each; --on-fail stop skips diagnosis",
		runs, prefix, verify.Round(time.Second))
}

// rerunRed re-runs the failed commands of a red tier once on the same merged
// tree before any bisect. A green re-run means the red is a flake: it is
// recorded in the flake ledger, reported as FLAKY naming the checks, no member
// is blamed and nothing lands. A second red returns done false and the bisect
// goes on with its culprit attribution. A CI train re-runs nothing locally.
func (t *trainRun) rerunRed(p trainPlan, res *landing.TrainResult, tier string, failed VerifyResult) (bool, error) {
	if p.onCI || len(failed.Failed) == 0 {
		return false, nil
	}
	e, ctx, scratch := t.e, t.ctx, p.scratch
	if _, code := e.git(scratch, "checkout", "-q", "-f", "--detach", p.tips[len(res.Members)]); code != 0 {
		return false, fmt.Errorf("git checkout %s failed in the scratch tree", p.tips[len(res.Members)])
	}
	tree := e.cleanVerifyTree(ctx, scratch)
	cmds := rerunCommands(failed, p.verifyCfg().rerunTemplate)
	t.phase("rerun %s: the red tree once before any bisect, %d command(s)", tier, len(cmds))
	began := time.Now()
	red, err := t.rerunIn(scratch, cmds)
	elapsed := time.Since(began)
	res.DiagnoseTime += elapsed
	if err != nil || red {
		return false, err
	}
	prs := []string{}
	for _, m := range res.Members {
		if m.PR != "" {
			prs = append(prs, m.PR)
		}
	}
	var entries []flakeledger.Entry
	for _, c := range failed.Failed {
		out := logSection(failed.Log, c)
		tests, _ := gotestout.FailingTests(out)
		entry := flakeledger.NewEntry(c, tests, tree, e.Now(),
			flakeledger.Run{Output: out, ExitCode: 1},
			flakeledger.Run{ExitCode: 0, WallSeconds: elapsed.Seconds()})
		entries = append(entries, entry)
		res.Flaky = append(res.Flaky, entry)
		res.Notes = append(res.Notes, flakeNote(entry))
	}
	if err := e.recordFlakes(t.root, t.o.Round, prs, VerifyResult{Flaky: entries}); err != nil {
		return false, err
	}
	res.Verdict, res.Unreproduced = landing.GateVerifyFailed, true
	res.Err = fmt.Sprintf("TRAIN-FAIL flaky — the train failed %s, then passed on a re-run of the same tree (%s); a flake, no bisect, no culprit, nothing landed\n%s",
		tier, strings.Join(failed.Failed, "; "), failed.detail())
	res.Hint = "nobody is blamed: re-run the train; if the same tests fail again the re-run goes red and the bisect names the culprit (see the FLAKY notes)"
	return true, nil
}

// stopTrain ends a red tier without diagnosis (OnFailStop): no prefix verify
// runs, no member is blamed, and the answer carries the evidence in res.Stop.
func (t *trainRun) stopTrain(res *landing.TrainResult, p trainPlan, tier, baseSHA string, failed VerifyResult) {
	st := &landing.TrainStop{Tier: tier, Command: strings.Join(failed.Failed, "; "), Scratch: p.scratch, Base: baseSHA}
	st.Tests, _ = gotestout.FailingTests(failed.Log)
	for i, m := range res.Members {
		st.Members = append(st.Members, landing.TrainStopMember{Target: m.Target, SHA: p.heads[i]})
	}
	res.Stop = st
	res.Verdict = landing.GateVerifyFailed
	var b strings.Builder
	fmt.Fprintf(&b, "TRAIN-FAIL — the train fails %s; no culprit attributed, diagnosis skipped (--on-fail stop); nothing landed\nfailed command: %s\n", tier, st.Command)
	if len(st.Tests) > 0 {
		fmt.Fprintf(&b, "failing tests: %s\n", strings.Join(st.Tests, ", "))
	}
	fmt.Fprintf(&b, "scratch tree: %s (removed on exit)\nbase: %s\n", st.Scratch, st.Base)
	for _, m := range st.Members {
		fmt.Fprintf(&b, "member %s: %s\n", m.Target, m.SHA)
	}
	res.Err = b.String() + failed.detail()
	res.Hint = "fix the failure shown, or re-run without --on-fail stop to bisect for the member"
}

// bisectTrain finds the first member whose merge makes tier fail, using verify
// on growing prefixes of the train (tips), and writes the verdict into res.
// failed is the failed full-train run; the caller hands its log over. It
// assumes the base is green: a red base is reported as such. passing is how
// many leading members may still land (all but the culprit under LandGreen);
// stop is true when the train must end here with res as the answer. classify
// then reruns the red alone (classifyRed): a red that passes on its member
// alone names the members that make it together, an interaction, or does not
// reproduce at all, a flake; either way it blames none of them.
func (e Env) bisectTrain(scratch string, res *landing.TrainResult, tips []string, tier string, verify func(int) (VerifyResult, error), classify func(k int, failed VerifyResult) (redClass, error), failed VerifyResult, landGreen bool) (passing int, stop bool, err error) {
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
	// green is the longest prefix that ran green: a diagnostic-only red prefix
	// moves lo past it but never makes it landable.
	lo, hi, green := 0, len(res.Members), 0
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
			lo, green = mid, mid
			continue
		}
		// A prefix failing only on what the original run never showed does not
		// reproduce it: keep looking later, the evidence stays the earlier run.
		if f := recordDiagnostic(res, tier, pr, mid, tips[mid]); res.Original != nil && !f.Reproduces(*res.Original) {
			os.Remove(pr.LogPath)
			lo = mid
			continue
		}
		os.Remove(failed.LogPath)
		failed, hi = pr, mid
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
			recordDiagnostic(res, tier, br, 0, tips[0])
			os.Remove(failed.LogPath)
			res.Verdict = landing.GateVerifyFailed
			res.Err = fmt.Sprintf("TRAIN-FAIL base — the base fails verification on its own (%s), so no member can be blamed\n%s",
				tier, br.detail())
			res.Hint = "fix the base, then re-run the train"
			return 0, true, nil
		}
	}
	class, err := classify(hi, failed)
	if err != nil {
		return 0, true, err
	}
	switch {
	case class.unreproduced:
		c := res.Members[hi-1]
		res.Verdict, res.Unreproduced = landing.GateVerifyFailed, true
		res.Err = fmt.Sprintf("TRAIN-FAIL unreproduced — the train fails %s once %s (%s) is merged, but %s passes alone and the failing tests passed on a rerun of the red prefix; a flake, no culprit, the first %d member(s) pass\n%s",
			tier, c.Target, c.Branch, c.Target, green, failed.detail())
		res.Hint = "nobody is blamed: re-run the train; if the same tests fail again, look at them (see the FLAKY notes)"
	case len(class.interaction) > 0:
		for _, i := range class.interaction {
			res.Interaction = append(res.Interaction, res.Members[i].Target)
		}
		names := strings.Join(res.Interaction, ", ")
		res.Verdict = landing.GateInteraction
		res.Err = fmt.Sprintf("TRAIN-INTERACTION %s — the train fails %s with %s merged together, though %s passes alone (base plus that member); no culprit, the first %d member(s) pass\n%s",
			names, tier, names, res.Members[hi-1].Target, green, failed.detail())
		res.Hint = fmt.Sprintf("hold %s and fix the interaction on one of them, then re-run the train; bounce neither", names)
	default:
		c := res.Members[hi-1]
		res.Members[hi-1].Culprit = true
		res.Culprit, res.Verdict = c.Target, landing.GateVerifyFailed
		res.Err = fmt.Sprintf("TRAIN-FAIL %s — the train fails %s once %s (%s) is merged; the first %d member(s) pass\n%s",
			c.Target, tier, c.Target, c.Branch, green, failed.detail())
		res.Hint = fmt.Sprintf("send %s back, or run the train without it; it may break only with the member(s) before it", c.Target)
	}
	if only := diagnosticOnly(res); len(only) > 0 {
		res.Err += fmt.Sprintf("\nDIAGNOSTIC-ONLY %s failed in a diagnostic prefix run but not in the original run; it is not confirmed as the cause of the original failure (no passing-alone check ran) and did not pick the culprit", strings.Join(only, ", "))
	}
	if !landGreen || green == 0 {
		return 0, true, nil
	}
	res.Hint += fmt.Sprintf("; the verified first %d member(s) landed (--land-green)", green)
	return green, false, nil
}

// recordDiagnostic appends a failed prefix run to res.Diagnostic, marking what
// the original run did not show (#2753).
func recordDiagnostic(res *landing.TrainResult, tier string, vr VerifyResult, members int, tip string) landing.TrainFailure {
	f := failureOf(tier, vr, members, tip)
	if res.Original != nil {
		f.OnlyInDiagnostic = f.OnlyIn(*res.Original)
	}
	res.Diagnostic = append(res.Diagnostic, f)
	return f
}

// diagnosticOnly lists, once each, everything a diagnostic run failed on that
// the original run did not.
func diagnosticOnly(res *landing.TrainResult) []string {
	var out []string
	for _, d := range res.Diagnostic {
		for _, x := range d.OnlyInDiagnostic {
			if !slices.Contains(out, x) {
				out = append(out, x)
			}
		}
	}
	return out
}

// trainFoot is what one member changes against the base: the paths (read as the
// overlap check reads them, `git diff --name-only base...head`) and the number
// of changed lines.
type trainFoot struct {
	paths []string
	lines int
}

func (e Env) trainFootprints(root, baseRef string, ms []landing.TrainMember, headRef func(landing.TrainMember) string) ([]trainFoot, error) {
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
func memberLabel(m landing.TrainMember) string {
	if n := prNumText(m.PR); n != "" {
		return "#" + n
	}
	return m.Target
}

func orderText(ms []landing.TrainMember) string {
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
func orderTrain(ms []landing.TrainMember, foots []trainFoot, override []string) (perm []int, lines []string, nIso int, err error) {
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
func conflictPair(ms []landing.TrainMember, foots []trainFoot, failed int) string {
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
func landedPRs(res *landing.TrainResult) []string {
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
func (t *trainRun) verifyLanded(p trainPlan, res *landing.TrainResult) (bool, error) {
	if !p.onCI && t.o.NoVerify && len(p.in.cfg.Full) == 0 && len(p.in.cfg.E2E) == 0 {
		return false, nil
	}
	full, err := p.verify(t.root)
	tier := p.tierRan
	if tier == "" {
		tier = tierFull
	}
	t.e.recordVerification(t.root, t.o.Round, tier, GateTarget{}, landedPRs(res), full)
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
		e2e, err := t.e.runE2E(t.ctx, p.in.cfg.E2E, t.root, p.in.ledger)
		if err == nil {
			t.e.recordVerification(t.root, t.o.Round, tierE2E, GateTarget{}, landedPRs(res), e2e)
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
	res.Verdict = landing.GateVerifyFailed
	res.Err = "landed train base does not pass verification: " + strings.Join(checks, ", ")
	res.Hint = "fix forward; rota round freeze names the blocked base and its repair"
	return true, nil
}
