package worker

import (
	"fmt"

	"github.com/l4ci/rota/internal/testledger"
)

// Verify tiers: the one place the gate and the merge train agree on what a
// tier's result means. The gate verifies a branch's scratch merge; the train
// verifies the whole train's. Both run test.full, then test.e2e, and both turn a
// tier's VerifyResult into the same answer: which commands count as verified,
// what was excused, and the note lines for an empty or red tier.
const (
	tierFull = "test.full"
	tierE2E  = "test.e2e"
)

// tierOutcome is what one verified tier says about the tree it ran on.
type tierOutcome struct {
	// NoCommands: test.full is empty, so nothing gated the tree by a command.
	NoCommands bool
	// Verified lists the commands that passed, Excluded the test-ledger entries
	// that excused a failing one. Both are empty when NoCommands.
	Verified []string
	Excluded []testledger.Entry
	// Notes are the lines to add to the result: NO-VERIFY for an empty
	// test.full, one "<label> FAILED: <cmd>" per failed command.
	Notes []string
	// OK: every command passed (or was excused). False when a command failed.
	// An empty tier is OK; NoCommands tells it apart.
	OK bool
}

// tierOutcomeOf reads vr, the result of running tier on a tree. subject names
// the gate's slot or "train" in the NO-VERIFY note.
func tierOutcomeOf(tier, subject string, vr VerifyResult) tierOutcome {
	if vr.NoCommands {
		return tierOutcome{NoCommands: true, OK: true, Notes: []string{noVerifyNote(subject), noVerifyHint}}
	}
	o := tierOutcome{Verified: vr.Verified, Excluded: vr.Excluded, OK: vr.OK()}
	label := "verify"
	if tier == tierE2E {
		label = "e2e"
	}
	for _, c := range vr.Failed {
		o.Notes = append(o.Notes, label+" FAILED: "+c)
	}
	return o
}

// noVerifyHint follows a NO-VERIFY note for an empty test.full.
const noVerifyHint = "set test.full via rota config set to make this gate real"

// noVerifyNote says the merged tree was not gated by test.full: subject is the
// slot, or "train".
func noVerifyNote(subject string) string {
	return fmt.Sprintf("NO-VERIFY %s — test.full is empty; the merged tree was NOT gated by a command.", subject)
}

// noVerifyFlagNote says --no-verify merged a tree no command gated.
func noVerifyFlagNote(subject string) string {
	return fmt.Sprintf("NO-VERIFY %s — --no-verify: merged tree was NOT gated by a command.", subject)
}

// noE2ENote says a land-green train landed prefix members without test.e2e,
// which never ran on that prefix.
func noE2ENote(passing int) string {
	return fmt.Sprintf("NO-E2E land-green — the verified first %d member(s) land without test.e2e; it never ran on that prefix.", passing)
}

// verifyTiers is the one runner of the tier sequence: test.full, then test.e2e,
// starting at first (the CI gate has run test.full already). An empty test.e2e
// is skipped; an empty test.full is visited, since only visit knows what an
// empty full means to its caller. visit runs one tier and says whether to go
// on: false ends the walk, as a red tier does. ok is true when every tier was
// visited and passed.
func verifyTiers(cfg GateConfig, first string, visit func(tier string, cmds []string) (bool, error)) (ok bool, err error) {
	tiers := []struct {
		name string
		cmds []string
	}{{tierFull, cfg.Full}, {tierE2E, cfg.E2E}}
	if first == tierE2E {
		tiers = tiers[1:]
	}
	for _, t := range tiers {
		if t.name == tierE2E && len(t.cmds) == 0 {
			continue
		}
		if ok, err := visit(t.name, t.cmds); !ok || err != nil {
			return false, err
		}
	}
	return true, nil
}

// tierWalk is the verify stage the gate and the train share: it runs the tiers
// in order, reads each result through tierOutcomeOf, records the outcome in its
// sinks and hands a red tier to red. The caller owns what differs: how a tier
// runs, what a run with no answer means, and what a red tier does.
type tierWalk struct {
	subject string // names the gate's slot, or the train, in the NO-VERIFY note
	run     func(tier string, cmds []string) (VerifyResult, error)
	// stop says a run gave no answer (CI not run, timed out). The caller sets its
	// verdict; true ends the walk.
	stop func(VerifyResult) bool
	// red handles a failed tier. stop ends the walk; false only ends the tier
	// sequence (a land-green train goes on with what passed).
	red func(tier string, vr VerifyResult) (stop bool, err error)
	// Sinks: where the outcome is recorded, in place. A nil e2eVerified discards
	// it (the gate keeps none).
	notes, verified, e2eVerified *[]string
	excluded                     *[]testledger.Entry

	// What the walk saw.
	at        string // tier being walked when it ended or errored
	fullEmpty bool   // test.full had no commands
	fullRed   bool   // test.full failed
	e2eGreen  bool   // test.e2e ran with commands and passed
}

// walk runs the tiers from first through the shared runner. ended is true when
// a callback or an error ended it; a red tier whose red says false is not ended.
func (w *tierWalk) walk(cfg GateConfig, first string) (ended bool, err error) {
	_, err = verifyTiers(cfg, first, func(tier string, cmds []string) (bool, error) {
		w.at = tier
		vr, err := w.run(tier, cmds)
		if err != nil {
			ended = true
			return false, err
		}
		end, ok, err := w.take(tier, vr)
		if end || err != nil {
			ended = true
		}
		return ok && !end && err == nil, err
	})
	return ended, err
}

// take records one tier's result, already run. ended is true when the walk is
// over; ok is true when the tier passed (or was empty) and the next may run.
func (w *tierWalk) take(tier string, vr VerifyResult) (ended, ok bool, err error) {
	w.at = tier
	full := tier == tierFull
	if w.stop != nil && w.stop(vr) {
		return true, false, nil
	}
	out := tierOutcomeOf(tier, w.subject, vr)
	*w.notes = append(*w.notes, out.Notes...)
	if out.NoCommands {
		w.fullEmpty = w.fullEmpty || full // nothing ran; test.e2e may
		return false, true, nil
	}
	if full {
		*w.verified = out.Verified
	} else if w.e2eVerified != nil {
		*w.e2eVerified = out.Verified
	}
	*w.excluded = append(*w.excluded, out.Excluded...)
	if out.OK {
		w.e2eGreen = w.e2eGreen || tier == tierE2E
		return false, true, nil
	}
	if full {
		w.fullRed = true
	}
	stop, err := w.red(tier, vr)
	return stop || err != nil, false, err
}
