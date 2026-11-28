package roundtick

import (
	"errors"

	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/landing"
	"github.com/l4ci/rota/internal/round"
)

// refusalScope is what an assign refusal means for the rest of the tick.
type refusalScope int

const (
	// scopeItem refuses this item only: the next one may go to the same slot.
	// It is the zero value, so a kind nobody classified costs one more try and
	// never leaves a slot idle behind one item.
	scopeItem refusalScope = iota
	// scopeSlot refuses the slot: assign-ahead leaves it alone.
	scopeSlot
	// scopeQuota is scopeSlot, and the tick says why it is capped.
	scopeQuota
	// scopeGate and scopeBatch hold every later assign of the tick.
	scopeGate
	scopeBatch
	// scopeNoRound is no refusal: the autopilot has no round to work for.
	scopeNoRound
	// scopeNone is an error that is no *round.BlockedError.
	scopeNone
)

// refusalScopes classifies every blockedBy kind an assign returns.
// TestRefusalKindsAreClassified fails on one that is missing.
var refusalScopes = map[string]refusalScope{
	blockedby.NotReady:       scopeItem,
	blockedby.Overlap:        scopeItem,
	blockedby.Claimed:        scopeItem,
	blockedby.ClaimLost:      scopeItem,
	blockedby.StaleClaim:     scopeItem,
	blockedby.NotStale:       scopeItem,
	blockedby.OpenPR:         scopeItem,
	blockedby.OutOfScope:     scopeItem,
	blockedby.Bundle:         scopeItem,
	blockedby.CrossMilestone: scopeItem,
	blockedby.HumanDecision:  scopeItem,
	blockedby.BriefMissing:   scopeItem,
	blockedby.Freeze:         scopeItem,
	blockedby.Site:           scopeItem,
	blockedby.Conflict:       scopeItem,
	blockedby.ModelLabel:     scopeItem,
	blockedby.HarnessLabel:   scopeItem,
	blockedby.DecisionLabel:  scopeItem,
	blockedby.BestOfLabel:    scopeItem,
	blockedby.BestOfSlots:    scopeItem,
	blockedby.NoTierMap:      scopeItem,

	blockedby.SlotBusy:        scopeSlot,
	blockedby.NoFreeSlot:      scopeSlot,
	blockedby.NoLogin:         scopeSlot,
	blockedby.Draining:        scopeSlot,
	blockedby.External:        scopeSlot,
	blockedby.ReviewNotMinted: scopeSlot,
	blockedby.Dirty:           scopeSlot,
	blockedby.Fetch:           scopeSlot,
	blockedby.Dispatch:        scopeSlot,
	// The worker launch preflight.
	blockedby.CodexFlags:     scopeSlot,
	blockedby.OpencodeFlags:  scopeSlot,
	blockedby.OpencodeBinary: scopeSlot,
	blockedby.OutsideHerdr:   scopeSlot,
	blockedby.Host:           scopeSlot,
	blockedby.ResumeFlag:     scopeSlot,
	blockedby.Limited:        scopeSlot,
	blockedby.Dialog:         scopeSlot,
	blockedby.ResetGuard:     scopeSlot,

	blockedby.Quota:     scopeQuota,
	landing.GateRunning: scopeGate,
	blockedby.Batch:     scopeBatch,
	blockedby.NoRound:   scopeNoRound,
}

// scopeOf is err's scope, with the refusal it carries (nil for scopeNone).
func scopeOf(err error) (refusalScope, *round.BlockedError) {
	var blk *round.BlockedError
	if !errors.As(err, &blk) {
		return scopeNone, nil
	}
	return refusalScopes[blk.By], blk
}

// isRefusal reports whether err is Assign declining without anything having
// gone wrong.
func isRefusal(err error) bool {
	scope, _ := scopeOf(err)
	return scope != scopeNone && scope != scopeNoRound
}
