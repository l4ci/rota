package worker

// VerdictMeaning is what one gate or train verdict means to a caller: whether
// it succeeds, whether the CLI turns it into an exit-4 refusal (and which
// blockedBy it names), whether it sends the PR back to its worker as a bounce,
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
	// Bounce: the PR goes back to its worker and the item's bounce count rises.
	Bounce bool
	// Hold: a refused target waits for a person. False for a verdict that
	// clears itself or goes back to a worker.
	Hold bool
}

// blockedBy keys the CLI treats specially when it builds a refusal envelope.
const (
	BlockedByVerdict  = "verdict"
	BlockedByNoVerify = "no-verify"
)

// verdictMeanings classifies every gate and train verdict. A verdict missing
// here fails TestVerdictsAreClassified.
var verdictMeanings = map[string]VerdictMeaning{
	GateFresh:            {Success: true, Hold: true},
	GatePass:             {Success: true, Hold: true},
	GateStale:            {Bounce: true},
	GatePRMismatch:       {Hold: true},
	GateProvenanceFail:   {Bounce: true},
	GateNotMerged:        {Hold: true},
	GateNotOnBase:        {Hold: true},
	GateVerifyFailed:     {Hold: true},
	GateMergedRemotely:   {Hold: true},
	GateMergeFailed:      {Hold: true},
	GateCheckBroke:       {Hold: true},
	GateCINotRun:         {Hold: true},
	GateVerifyTimeout:    {Hold: true},
	GateCIConfigChanged:  {Hold: true},
	GateBaseMoved:        {Hold: true},
	GateApprovalRequired: {Refusal: true, ViaError: true, Hold: true},
	GateVerdictBlocked:   {Refusal: true, ViaError: true, BlockedBy: BlockedByVerdict, Hold: true},
	GateNoVerify:         {Refusal: true, BlockedBy: BlockedByNoVerify, Hold: true},
	GateNotClosing:       {Refusal: true, BlockedBy: GateNotClosing, Hold: true},
	GateBestOfUnpicked:   {Refusal: true, BlockedBy: GateBestOfUnpicked},
	GateReviewMissing:    {Refusal: true, BlockedBy: GateReviewMissing, Hold: true},
	GateOrder:            {Refusal: true, BlockedBy: GateOrder, Hold: true},
}

// ClassifyVerdict says what a verdict means. A verdict nobody classified is a
// plain exit-1 refusal that waits for a person, the safe reading.
func ClassifyVerdict(verdict string) VerdictMeaning {
	if m, ok := verdictMeanings[verdict]; ok {
		return m
	}
	return VerdictMeaning{Hold: true}
}
