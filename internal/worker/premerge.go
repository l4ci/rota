package worker

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/land"
	"github.com/l4ci/rota/internal/testledger"
)

// The pre-merge policy of the gate and the train, owned here. Both refuse in
// this order, and a rule changes in this file only:
//
//  1. ledger expiry  (ledgerRefusal)
//  2. no-verify      (noVerifyRefusalFor)
//  3. CI-config      (ciConfigCheck)
//  4. approval       (the caller's Approve hook: verdict approval-required)
//
// Both run the sequence through premergeRun.run, the gate at its stepPremerge
// and the train before its scratch merge. What differs between them is only the
// wording, carried by a premergeSubject, and the inputs each has. A merge
// failure maps to a message in mergeRefusal.

// premergeSubject words the refusals for whoever runs the checks.
type premergeSubject struct {
	// Fail leads the ledger refusal: "GATE-FAIL <slot> —" or "TRAIN-FAIL".
	Fail string
	// What names the actor in the no-verify refusal: "GATE <slot>" or "TRAIN".
	What string
	// CIWho names the actor in the CI-config refusal: the slot, or "train".
	CIWho string
	// Retry is what to do after fixing the ledger: "re-gate", "re-run the train".
	Retry string
}

// gateSubject words the gate's refusals for slot.
func gateSubject(slot string) premergeSubject {
	return premergeSubject{Fail: "GATE-FAIL " + slot + " —", What: "GATE " + slot, CIWho: slot, Retry: "re-gate"}
}

// trainSubject words the train's refusals.
var trainSubject = premergeSubject{Fail: "TRAIN-FAIL", What: "TRAIN", CIWho: "train", Retry: "re-run the train"}

// premergeRefusal is a pre-merge check that refused: the verdict, message and
// hint to set on the result. Expired is set for a ledger refusal.
type premergeRefusal struct {
	Verdict, Err, Hint string
	Expired            []testledger.Entry
}

// ledgerRefusal refuses a run while a test-ledger entry has expired, or
// returns nil.
func ledgerRefusal(s premergeSubject, led testledger.Ledger, now time.Time) *premergeRefusal {
	msg := LedgerExpiry(led, now)
	if msg == "" {
		return nil
	}
	return &premergeRefusal{
		Verdict: GateVerifyFailed,
		Err:     fmt.Sprintf("%s %s; nothing landed", s.Fail, msg),
		Hint:    "fix the test or renew the entry in .rota/test-ledger.json, then " + s.Retry,
		Expired: led.Expired(now),
	}
}

// noVerifyRefusalFor refuses a run whose merge would land with nothing
// verifying it (see noVerifyRule), or returns nil.
func noVerifyRefusalFor(s premergeSubject, in gateInput) *premergeRefusal {
	if !noVerifyRule(in.cfg.where, in.cfg.Full, in.cfg.E2E) {
		return nil
	}
	msg, hint := noVerifyRefusal(s.What)
	return &premergeRefusal{Verdict: GateNoVerify, Err: msg, Hint: hint}
}

// ciConfigCheck refuses a merge that changes the CI definition, or returns nil.
func ciConfigCheck(s premergeSubject, changed []string) *premergeRefusal {
	msg, hint := ciConfigRefusal(s.CIWho, changed)
	if msg == "" {
		return nil
	}
	return &premergeRefusal{Verdict: GateCIConfigChanged, Err: msg, Hint: hint}
}

// premergeRun is one run of the shared pre-merge sequence.
type premergeRun struct {
	Subject premergeSubject
	In      gateInput
	Now     time.Time
	// SkipLedger skips the ledger-expiry check: the gate does not verify under
	// --no-verify or --check-only, so an expired entry does not matter to it.
	SkipLedger bool
	// SkipNoVerify skips the no-verify check, which --no-verify answers.
	SkipNoVerify bool
	// CIChanged lists the paths the merge changes for the CI-config check. Nil
	// skips the check: the merge is not verified on CI.
	CIChanged func() ([]string, error)
	// Approve is the merge-approval gate (B1), asked with Files. Nil skips it.
	Approve func(files func() ([]string, error)) error
	Files   func() ([]string, error)
}

// run walks the sequence and stops at the first refusal. A refusal is returned
// as such; an error is a check that could not run (CIChanged) or the approval
// gate's own refusal, with approval true.
func (p premergeRun) run() (r *premergeRefusal, approval bool, err error) {
	if !p.SkipLedger {
		if r := ledgerRefusal(p.Subject, p.In.ledger, p.Now); r != nil {
			return r, false, nil
		}
	}
	if !p.SkipNoVerify {
		if r := noVerifyRefusalFor(p.Subject, p.In); r != nil {
			return r, false, nil
		}
	}
	if p.CIChanged != nil {
		changed, err := p.CIChanged()
		if err != nil {
			return nil, false, err
		}
		if r := ciConfigCheck(p.Subject, changed); r != nil {
			return r, false, nil
		}
	}
	if p.Approve != nil {
		if err := p.Approve(p.Files); err != nil {
			return nil, true, err
		}
	}
	return nil, false, nil
}

// mergeWords is how a caller words a failed merge.
type mergeWords struct {
	// Failed leads a failure message; the exit code and output follow it.
	Failed string
	// Conflict and ConflictHint are the message and hint for a real conflict.
	Conflict, ConflictHint string
}

// mergeRefusal maps a land.MergeLocal error to its message and hint. Only a
// real conflict is called one: anything else (no committer identity, a hook, a
// locked index) is reported in git's own words, so it is not mistaken for work
// to resolve with the slot.
func mergeRefusal(err error, w mergeWords) (msg, hint string) {
	var me *land.MergeError
	switch {
	case errors.As(err, new(*land.CleanupError)):
		return fmt.Sprintf("%s: %s", w.Failed, err), ""
	case errors.As(err, new(*land.ConflictError)):
		return w.Conflict, w.ConflictHint
	case errors.As(err, &me):
		return fmt.Sprintf("%s (exit %d): %s", w.Failed, me.Code, strings.TrimSpace(me.Out)), ""
	default:
		return fmt.Sprintf("%s (exit 127): %s", w.Failed, err), ""
	}
}
