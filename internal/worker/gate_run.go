package worker

import (
	"cmp"
	"context"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/landing"
	"github.com/l4ci/rota/internal/tracker"
	"slices"
)

// run wires each step's named output to the inputs that consume it. No step
// receives another step's operation-local scratch fields.
func (g gateRun) run(target GateTarget, result landing.GateResult) (landing.GateResult, error) {
	var err error
	if g.o.ReviewsOnly {
		result, err = g.runReviews(target, result)
	} else {
		result, err = g.runGate(target, result)
	}
	if err != nil {
		return result, err
	}
	// Bookkeeping runs after terminal verdicts too, using only the public answer.
	target.Branch, target.PR, target.Issue = result.Branch, result.PR, result.Issue
	start := landingStageResult[struct{}, landing.GateResult]{Result: result}
	cleared := runGateStage(start, func(result landing.GateResult) (struct{}, landing.GateResult, bool, error) {
		return g.stepClearFreeze(result)
	})
	recorded := runGateStage(cleared, func(result landing.GateResult) (struct{}, landing.GateResult, bool, error) {
		return g.stepRecordLedger(result)
	})
	dequeued := runGateStage(recorded, func(result landing.GateResult) (struct{}, landing.GateResult, bool, error) {
		return g.stepDequeue(result, target.PR)
	})
	released := runGateStage(dequeued, func(result landing.GateResult) (struct{}, landing.GateResult, bool, error) {
		return g.stepReleaseExternal(result, target)
	})
	return released.Result, released.Err
}

func (g gateRun) runGate(target GateTarget, result landing.GateResult) (landing.GateResult, error) {
	start := landingStageResult[struct{}, landing.GateResult]{Result: result}
	forged := runGateStage(start, func(result landing.GateResult) (gateForgeResult, landing.GateResult, bool, error) {
		return g.stepForge(result, target)
	})
	external := runGateStage(forged, func(result landing.GateResult) (GateTarget, landing.GateResult, bool, error) {
		return g.stepExternal(result, target, forged.Value)
	})
	adopted := runGateStage(external, func(result landing.GateResult) (GateTarget, landing.GateResult, bool, error) {
		return g.stepAdoptPR(result, external.Value, forged.Value)
	})
	onIntegration := runGateStage(adopted, func(result landing.GateResult) (struct{}, landing.GateResult, bool, error) {
		return g.stepIntegrationBase(result, adopted.Value)
	})
	frozen := runGateStage(onIntegration, func(result landing.GateResult) (struct{}, landing.GateResult, bool, error) {
		return g.stepFreeze(result, adopted.Value.Issue, adopted.Value.PR)
	})
	picked := runGateStage(frozen, func(result landing.GateResult) (struct{}, landing.GateResult, bool, error) {
		return g.stepBestOf(result, adopted.Value.Issue, adopted.Value.PR)
	})
	remote := runGateStage(picked, func(result landing.GateResult) (bool, landing.GateResult, bool, error) {
		return g.stepRemote(result, adopted.Value.PR)
	})
	inspected := runGateStage(remote, func(result landing.GateResult) (*tracker.PRInfo, landing.GateResult, bool, error) {
		return g.stepAlreadyMerged(result, adopted.Value, forged.Value, remote.Value)
	})
	followed := runGateStage(inspected, func(result landing.GateResult) (gateSlotHeadResult, landing.GateResult, bool, error) {
		return g.stepSlotHead(result, adopted.Value, forged.Value, remote.Value, inspected.Value)
	})
	refs := runGateStage(followed, func(result landing.GateResult) (gateRefsResult, landing.GateResult, bool, error) {
		return g.stepRefs(result, followed.Value.target.Branch, remote.Value)
	})
	attempt := runGateStage(refs, func(result landing.GateResult) (struct{}, landing.GateResult, bool, error) {
		return g.stepAttempt(result, followed.Value.target, remote.Value, refs.Value)
	})
	outcome := runGateStage(attempt, func(result landing.GateResult) (struct{}, landing.GateResult, bool, error) {
		return g.stepOutcome(result, followed.Value.target, forged.Value, refs.Value.head)
	})
	fresh := runGateStage(outcome, func(result landing.GateResult) (struct{}, landing.GateResult, bool, error) {
		return g.stepFreshness(result, followed.Value.target, forged.Value, remote.Value, refs.Value)
	})
	matched := runGateStage(fresh, func(result landing.GateResult) (string, landing.GateResult, bool, error) {
		return g.stepPRMatches(result, followed.Value.target, forged.Value, remote.Value, refs.Value, followed.Value.viewed)
	})
	reports := runGateStage(matched, func(result landing.GateResult) (struct{}, landing.GateResult, bool, error) {
		return g.stepVerdictReports(result, followed.Value.target, forged.Value, remote.Value, refs.Value, matched.Value)
	})
	provenance := runGateStage(reports, func(result landing.GateResult) (struct{}, landing.GateResult, bool, error) {
		return g.stepProvenance(result, followed.Value.target, forged.Value, refs.Value.head)
	})
	closes := runGateStage(provenance, func(result landing.GateResult) (struct{}, landing.GateResult, bool, error) {
		return g.stepCloses(result, followed.Value.target, forged.Value, refs.Value.head)
	})
	claim := runGateStage(closes, func(result landing.GateResult) (struct{}, landing.GateResult, bool, error) {
		return g.stepClaim(result, followed.Value.target, forged.Value, remote.Value)
	})
	verdict := runGateStage(claim, func(result landing.GateResult) (struct{}, landing.GateResult, bool, error) {
		return g.stepVerdict(result, followed.Value.target, forged.Value, refs.Value)
	})
	depth := runGateStage(verdict, func(result landing.GateResult) (struct{}, landing.GateResult, bool, error) {
		return g.stepReviewDepth(result, followed.Value.target, forged.Value, refs.Value, matched.Value)
	})
	listed := runGateStage(depth, func(result landing.GateResult) (struct{}, landing.GateResult, bool, error) {
		return g.stepGaps(result, followed.Value.target, forged.Value)
	})
	checked := runGateStage(listed, func(result landing.GateResult) (struct{}, landing.GateResult, bool, error) {
		return g.stepCheckOnly(result, followed.Value.target, forged.Value, remote.Value, refs.Value, matched.Value)
	})
	onBase := runGateStage(checked, func(result landing.GateResult) (struct{}, landing.GateResult, bool, error) {
		return g.stepOnBase(result)
	})
	ci := runGateStage(onBase, func(result landing.GateResult) (*ciVerifier, landing.GateResult, bool, error) {
		return g.stepCIVerifier(result, forged.Value)
	})
	premerge := runGateStage(ci, func(result landing.GateResult) (struct{}, landing.GateResult, bool, error) {
		return g.stepPremerge(result, followed.Value.target, refs.Value, matched.Value, ci.Value)
	})
	mergeable := runGateStage(premerge, func(result landing.GateResult) (struct{}, landing.GateResult, bool, error) {
		return g.stepForgeMergeable(result, followed.Value.target, forged.Value, remote.Value)
	})
	tier := runGateStage(mergeable, func(result landing.GateResult) (*GateConfig, landing.GateResult, bool, error) {
		return g.stepTier(result, followed.Value.target, forged.Value, refs.Value, matched.Value, ci.Value)
	})
	verified := runGateStage(tier, func(result landing.GateResult) (gateVerification, landing.GateResult, bool, error) {
		return g.stepVerifyFirst(result, followed.Value.target, forged.Value, remote.Value, refs.Value, matched.Value, ci.Value, tier.Value)
	})
	merged := runGateStage(verified, func(result landing.GateResult) (string, landing.GateResult, bool, error) {
		return g.stepMerge(result, followed.Value.target, forged.Value, remote.Value, refs.Value, matched.Value)
	})
	confirmed := runGateStage(merged, func(result landing.GateResult) (struct{}, landing.GateResult, bool, error) {
		return g.stepVerify(result, followed.Value.target, forged.Value, remote.Value, refs.Value, matched.Value, verified.Value, merged.Value)
	})
	return gapsFirst(confirmed.Result, confirmed.Err)
}

// gapsFirst keeps a record gap ahead of a refusal a later check raised: the
// gate used to stop at the gap, so the later check never ran. A hard refusal
// that is itself a gap's verdict, or a success, passes through.
func gapsFirst(res landing.GateResult, err error) (landing.GateResult, error) {
	if len(res.Gaps) == 0 || res.Verdict == res.Gaps[0].Verdict {
		return res, err
	}
	if err != nil && res.Verdict != landing.GateVerdictBlocked {
		return res, err // a failed read, not a refusal
	}
	refuseGateGaps(&res)
	return res, nil
}

func (g gateRun) runReviews(target GateTarget, result landing.GateResult) (landing.GateResult, error) {
	start := landingStageResult[struct{}, landing.GateResult]{Result: result}
	forged := runGateStage(start, func(result landing.GateResult) (gateForgeResult, landing.GateResult, bool, error) {
		return g.stepForge(result, target)
	})
	external := runGateStage(forged, func(result landing.GateResult) (GateTarget, landing.GateResult, bool, error) {
		return g.stepExternal(result, target, forged.Value)
	})
	adopted := runGateStage(external, func(result landing.GateResult) (GateTarget, landing.GateResult, bool, error) {
		return g.stepAdoptPR(result, external.Value, forged.Value)
	})
	frozen := runGateStage(adopted, func(result landing.GateResult) (struct{}, landing.GateResult, bool, error) {
		return g.stepFreeze(result, adopted.Value.Issue, adopted.Value.PR)
	})
	remote := runGateStage(frozen, func(result landing.GateResult) (bool, landing.GateResult, bool, error) {
		return g.stepRemote(result, adopted.Value.PR)
	})
	inspected := runGateStage(remote, func(result landing.GateResult) (*tracker.PRInfo, landing.GateResult, bool, error) {
		return g.stepAlreadyMerged(result, adopted.Value, forged.Value, remote.Value)
	})
	followed := runGateStage(inspected, func(result landing.GateResult) (gateSlotHeadResult, landing.GateResult, bool, error) {
		return g.stepSlotHead(result, adopted.Value, forged.Value, remote.Value, inspected.Value)
	})
	refs := runGateStage(followed, func(result landing.GateResult) (gateRefsResult, landing.GateResult, bool, error) {
		return g.stepRefs(result, followed.Value.target.Branch, remote.Value)
	})
	matched := runGateStage(refs, func(result landing.GateResult) (string, landing.GateResult, bool, error) {
		return g.stepPRMatches(result, followed.Value.target, forged.Value, remote.Value, refs.Value, followed.Value.viewed)
	})
	reports := runGateStage(matched, func(result landing.GateResult) (struct{}, landing.GateResult, bool, error) {
		return g.stepVerdictReports(result, followed.Value.target, forged.Value, remote.Value, refs.Value, matched.Value)
	})
	depth := runGateStage(reports, func(result landing.GateResult) (struct{}, landing.GateResult, bool, error) {
		return g.stepReviewDepth(result, followed.Value.target, forged.Value, refs.Value, matched.Value)
	})
	listed := runGateStage(depth, func(result landing.GateResult) (struct{}, landing.GateResult, bool, error) {
		return g.stepGaps(result, followed.Value.target, forged.Value)
	})
	checked := runGateStage(listed, func(result landing.GateResult) (struct{}, landing.GateResult, bool, error) {
		return g.stepCheckOnly(result, followed.Value.target, forged.Value, remote.Value, refs.Value, matched.Value)
	})
	return checked.Result, checked.Err
}

// git runs git on the gate's context.
func (g gateRun) git(dir string, args ...string) (string, int) {
	return g.e.runGit(g.ctx, dir, args...)
}

// isAncestor is Env.isAncestor on the gate's context.
func (g gateRun) isAncestor(dir, ancestor, descendant string) (bool, error) {
	return git.IsAncestorWith(func(args ...string) (int, error) {
		_, code := g.git(dir, args...)
		return code, nil
	}, ancestor, descendant)
}

// gateForgeResult is the forge connection opened by stepForge. It has no
// target, refs, verifier or result fields that later steps could secretly set.
type gateForgeResult struct {
	provider     string
	forge        Forge
	issues       Issues
	local        bool
	cliName      string
	closedLabels []string
}

type gateSlotHeadResult struct {
	target GateTarget
	viewed *tracker.PRInfo
}

type gateRefsResult struct {
	head, base string
	localHead  string
}

type gateVerification struct {
	tree  string
	ciRun *VerifyResult
}

// gateRun is the one state type for gate steps and their helpers. The run
// carries configuration and dependencies; operation gives each step its own
// answer and scratch fields, preserving the explicit handoffs between stages.
type gateRun struct {
	e                          Env
	ctx                        context.Context
	root                       string
	o                          GateOpts
	in                         gateInput
	resolved                   GateTarget // original target retained by the ledger
	res                        *landing.GateResult
	target                     GateTarget
	branch, pr, prNum          string
	provider                   string
	forge                      Forge
	issues                     Issues
	local                      bool
	cliName                    string
	closedLabels               []string
	sectionBody                *string
	viewed                     *tracker.PRInfo
	remote                     bool
	headRef, baseRef, verified string
	tierCfg                    *GateConfig
	ci                         *ciVerifier
	verifiedTree               string
	ciRun                      *VerifyResult
	landed                     string
}

func (g gateRun) operation(result landing.GateResult, target GateTarget, forged gateForgeResult) gateRun {
	// A step owns its answer, including append capacity. A shallow struct copy
	// would let it overwrite a caller's slice when that slice has spare capacity.
	result.Notes = slices.Clone(result.Notes)
	result.Verified = slices.Clone(result.Verified)
	result.Flaky = slices.Clone(result.Flaky)
	result.Skipped = slices.Clone(result.Skipped)
	result.Warnings = slices.Clone(result.Warnings)
	result.Excluded = slices.Clone(result.Excluded)
	result.Expired = slices.Clone(result.Expired)
	return gateRun{e: g.e, ctx: g.ctx, root: g.root, o: g.o, in: g.in, resolved: g.resolved, res: &result, target: target,
		branch: target.Branch, pr: target.PR, prNum: prNumText(target.PR),
		provider: forged.provider, forge: forged.forge, issues: forged.issues,
		local: forged.local, cliName: forged.cliName, closedLabels: forged.closedLabels}
}

// verdict records a non-success verdict.
func (g *gateRun) verdict(v, msg, hint string) {
	g.res.Verdict, g.res.Err, g.res.Hint = v, msg, hint
}

// gap records a record gap without ending the gate: the record checks all run,
// and stepGaps refuses once with every gap listed.
func (g *gateRun) gap(v, msg, hint string) {
	g.res.Gaps = append(g.res.Gaps, landing.GateGap{BlockedBy: cmp.Or(landing.ClassifyVerdict(v).BlockedBy, v), Msg: msg, Hint: hint, Verdict: v})
}

func (g *gateRun) broke(msg string) (bool, error) {
	g.verdict(landing.GateCheckBroke, checkBrokeMessage(g.o.Slot, msg), "")
	return true, nil
}

// gitRunner adapts Env.Git in dir to verdict.Runner.
type gitRunner struct {
	ctx context.Context
	e   Env
	dir string
}
