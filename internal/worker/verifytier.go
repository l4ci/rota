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
