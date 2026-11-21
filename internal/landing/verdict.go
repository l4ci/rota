package landing

import "github.com/l4ci/rota/internal/blockedby"

// VerdictMeaning is what one gate or train verdict means to a caller: whether
// it succeeds, whether the CLI turns it into an exit-4 refusal (and which
// blockedBy it names), whether it sends the PR back to its worker uncounted,
// and whether a refused target then waits for a person.
type VerdictMeaning struct {
	// Success: the gate or train passed (fresh under CheckOnly, pass).
	Success bool
	// Refusal: the CLI exits 4 instead of 1 with nothing landed.
	Refusal bool
	// BlockedBy is the refusal's blockedBy key. Empty for a refusal whose
	// envelope comes from the error the gate returned with it (approval-required).
	BlockedBy string
	// ViaError: the refusal travels with a non-nil error from the gate. Every
	// other refusal arrives with a nil error.
	ViaError bool
	// Notify: the PR goes back to its worker without counting. A stale merge and
	// a provenance failure are bookkeeping the worker repairs, not review
	// findings, so they never count toward round.maxBounces or park the item
	// (#2072); a support machine's worker still learns of them on its PR.
	Notify bool
	// Hold: a refused target waits for a person. False for a verdict that
	// clears itself or goes back to a worker.
	Hold bool
}

// BlockedByVerdict is the blockedBy key the CLI treats specially when it builds a
// refusal envelope; the other special key, no-verify, is the GateNoVerify verdict.
const BlockedByVerdict = blockedby.Verdict

// verdictMeanings classifies every gate and train verdict. A verdict missing
// here fails TestVerdictsAreClassified.
var verdictMeanings = map[string]VerdictMeaning{
	GateFreeze:             {Refusal: true, BlockedBy: blockedby.Freeze, Hold: true},
	GateFresh:              {Success: true, Hold: true},
	GatePass:               {Success: true, Hold: true},
	GateStale:              {Notify: true},
	GatePRMismatch:         {Hold: true},
	GateProvenanceFail:     {Notify: true},
	GateNotMerged:          {Hold: true},
	GateNotOnBase:          {Hold: true},
	GateVerifyFailed:       {Hold: true},
	GateInteraction:        {Hold: true},
	GateMergedRemotely:     {Hold: true},
	GateMergeFailed:        {Hold: true},
	GateCheckBroke:         {Hold: true},
	GateCancelled:          {}, // stopped on request: no finding, nobody waits on it
	GateCINotRun:           {Hold: true},
	GateVerifyTimeout:      {Hold: true},
	GateCIConfigChanged:    {Hold: true},
	GateBaseMoved:          {Hold: true},
	GateApprovalRequired:   {Refusal: true, ViaError: true, Hold: true},
	GateVerdictBlocked:     {Refusal: true, ViaError: true, BlockedBy: BlockedByVerdict, Hold: true},
	GateVacuousTests:       {Refusal: true, BlockedBy: blockedby.VacuousTests, Hold: true},
	GateNoVerify:           {Refusal: true, BlockedBy: blockedby.NoVerify, Hold: true},
	GateNotClosing:         {Refusal: true, BlockedBy: blockedby.Closes, Hold: true},
	GateAssumptionsMissing: {Refusal: true, BlockedBy: blockedby.AssumptionsMissing, Hold: true},
	GateRulingsMissing:     {Refusal: true, BlockedBy: blockedby.RulingsMissing, Hold: true},
	GateUnmetAcceptance:    {Refusal: true, BlockedBy: blockedby.UnmetAcceptance, Hold: true},
	GateClaimLost:          {Refusal: true, BlockedBy: blockedby.ClaimLost, Hold: true},
	GateBestOfUnpicked:     {Refusal: true, BlockedBy: blockedby.BestOfUnpicked},
	GateReviewMissing:      {Refusal: true, BlockedBy: blockedby.ReviewMissing, Hold: true},
	GateStaleAttempt:       {Refusal: true, BlockedBy: blockedby.StaleAttempt, Hold: true},
	GateOutcomeOpen:        {Refusal: true, BlockedBy: blockedby.OutcomeOpen, Hold: true},
	GateRunning:            {Refusal: true, BlockedBy: blockedby.GateRunning, Hold: true},
	GateForge:              {Refusal: true, BlockedBy: blockedby.Forge, Hold: true},
	GateAlreadyMerged:      {Success: true},
	GatePlan:               {Success: true},
	GateOrder:              {Refusal: true, BlockedBy: blockedby.Order, Hold: true},
}

// ClassifyVerdict says what a verdict means. A verdict nobody classified is a
// plain exit-1 refusal that waits for a person, the safe reading.
func ClassifyVerdict(verdict string) VerdictMeaning {
	if m, ok := verdictMeanings[verdict]; ok {
		return m
	}
	return VerdictMeaning{Hold: true}
}

// refused reports whether a gate or train verdict, with the error it came
// with, is one the CLI turns into an exit-4 refusal.
func (m VerdictMeaning) refused(err error) bool {
	return m.Refusal && m.ViaError == (err != nil)
}

// Refused reports whether the gate's verdict, with the error Gate returned, is
// a refusal: nothing landed and the CLI answers exit 4.
func (r GateResult) Refused(err error) bool { return ClassifyVerdict(r.Verdict).refused(err) }

// Refused is GateResult.Refused for the train.
func (r TrainResult) Refused(err error) bool { return ClassifyVerdict(r.Verdict).refused(err) }

const (
	GateFreeze          = blockedby.Freeze
	GateUnmetAcceptance = blockedby.UnmetAcceptance
	// GateBaseMoved: the base or a member's head moved while the train verified, so
	// the verified tree is no longer what would land. Nothing landed.
	GateBaseMoved = "base-moved"
	// GateOrder: a shared-path member conflicts with the members before it; the CLI
	// exits 4 with it, blockedBy order. Nothing was pushed.
	GateOrder = blockedby.Order
	// GatePlan: the verdict of a --plan answer. Nothing was verified or landed;
	// the CLI exits 0 with the conflict map.
	GatePlan            = "plan"
	GateCINotRun        = "ci-not-run"
	GateVerifyTimeout   = "verify-timeout"
	GateCIConfigChanged = "ci-config-changed"
	GateFresh           = "fresh"
	GatePass            = "pass"
	GateStale           = "stale"
	GatePRMismatch      = "pr-mismatch"
	GateProvenanceFail  = "provenance-fail"
	GateNotMerged       = "not-merged"
	GateNotOnBase       = "not-on-base"
	GateVerifyFailed    = "verify-failed"
	// GateInteraction: a train's red test passes on each member alone but
	// fails with them merged together (#3095). No member is the culprit.
	GateInteraction = "interaction"
	// GateVacuousTests refuses tests independent of the non-test change.
	GateVacuousTests   = blockedby.VacuousTests
	GateMergedRemotely = "merged-remotely"
	GateMergeFailed    = "merge-failed"
	GateCheckBroke     = "check-broke"
	// GateCancelled: the train was cancelled (rota worker train cancel, SIGINT
	// or SIGTERM) before it finished. Not a code or test failure.
	GateCancelled = "cancelled"
	// GateApprovalRequired: the merge-approval gate (B1) refused before the
	// merge; the CLI exits 4 with it.
	GateApprovalRequired = "approval-required"
	// GateVerdictBlocked: a recorded FAIL verdict (B3) refused before the
	// merge; the CLI exits 4 with it, blockedBy verdict.
	GateVerdictBlocked = "verdict-blocked"
	// GateNoVerify: test.full is empty, so a merge would land unverified; the
	// CLI exits 4 with it unless --no-verify was passed. The verdict is also
	// the refusal's blockedBy key.
	GateNoVerify = blockedby.NoVerify
	// GateNotClosing: the PR body has no closing keyword for the slot's issue,
	// so the merge would leave it open and drifted; the CLI exits 4 with it,
	// blockedBy closes. An issue labelled partial-slice is exempt, but a PR
	// the forge says closes one is refused with the same verdict (FinalSlice).
	GateNotClosing = blockedby.Closes
	// GateClaimLost fences a holder whose issue was reclaimed elsewhere.
	GateClaimLost = blockedby.ClaimLost
	// GateBestOfUnpicked: the PR is an attempt of a best-of:2 issue and no
	// `rota round pick` names it; the CLI exits 4 with it, blockedBy best-of-unpicked.
	GateBestOfUnpicked = blockedby.BestOfUnpicked
	// GateReviewMissing: the review depth ship.review resolves for the branch
	// needs a recorded verdict the branch lacks; the CLI exits 4 with it,
	// blockedBy review-missing.
	GateReviewMissing = blockedby.ReviewMissing
	// GateStaleAttempt: a commit on the PR branch is stamped with an attempt
	// the slot no longer holds, so a worker whose assignment was reclaimed or
	// transferred pushed it; the CLI exits 4 with it, blockedBy stale-attempt.
	GateStaleAttempt = blockedby.StaleAttempt
	// GateOutcomeOpen: the slot's worker reported a partial or failed outcome
	// and no relay or transfer has answered it; the CLI exits 4 with it,
	// blockedBy outcome-open.
	GateOutcomeOpen = blockedby.OutcomeOpen
	// GateRunning: another gate for this PR is running or queued, or a NoWait
	// run yielded to other landing work. The CLI exits 4, blockedBy gate-running,
	// before this run takes the land lock.
	GateRunning = blockedby.GateRunning
	// GateForge: the origin names no forge and forge.provider names none, so
	// the gate cannot tell where the PR lives (tracker.ResolveProvider, an
	// actor); the CLI exits 4 with it, blockedBy forge.
	GateForge = blockedby.Forge
	// GateAlreadyMerged: the PR merged before this gate checked its head.
	// A success that changes nothing.
	GateAlreadyMerged      = "already-merged"
	GateAssumptionsMissing = blockedby.AssumptionsMissing
	GateRulingsMissing     = blockedby.RulingsMissing
)
