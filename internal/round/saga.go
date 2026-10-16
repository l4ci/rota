package round

import (
	"fmt"
	"strings"
)

// step is one unit of a saga: a change plus the compensation that takes it
// back. Assign, Transfer, Return and Reclaim are ordered lists of steps run by runSteps, so each
// step carries its own resume check and undo instead of the flow doing it inline.
type step struct {
	name string
	// skip reports the step already done (or not needed): a repeated call
	// resumes by skipping what an earlier one finished. Nil never skips.
	skip func() bool
	do   func() error
	// undo takes the step's change back. It runs when a LATER step fails and
	// the saga rolls back, and also for a step that was skipped: a resumed call
	// that fails rolls back what the first call left. Nil: nothing to take back.
	undo func() error
	// keep makes a failure of this step leave the earlier steps in place: the
	// step's effect may already be out in the world (a pane that holds the
	// brief), so rolling back would strand it. The failed step cleans up after
	// itself inside do.
	keep bool
}

// StageError is a saga failure that names where it stopped: the failed stage
// and the stages whose effect is still in place (an undo that failed, or every
// earlier stage of a keep step). A receipt stores both so a retry knows what
// the first call left.
type StageError struct {
	Stage    string
	Residual []string
	Err      error
}

func (e *StageError) Error() string { return e.Err.Error() }
func (e *StageError) Unwrap() error { return e.Err }

// runSteps runs steps in order and returns the first error. A failed step is
// not undone (it leaves nothing behind itself); unless it is keep, the steps
// before it are undone, latest first. An undo that fails is reported with the
// step's own error: a rollback that left a claim or state behind is not silent.
func runSteps(steps []step) error {
	var ran []string // stages this call finished, in order
	for i, s := range steps {
		if s.skip != nil && s.skip() {
			continue
		}
		if err := s.do(); err != nil {
			se := &StageError{Stage: s.name, Err: err}
			if s.keep {
				se.Residual = ran
			} else {
				var left []string
				for j := i - 1; j >= 0; j-- {
					if u := steps[j].undo; u != nil {
						if uerr := u(); uerr != nil {
							left = append(left, steps[j].name+": "+uerr.Error())
							se.Residual = append(se.Residual, steps[j].name)
						}
					}
				}
				if len(left) > 0 {
					se.Err = fmt.Errorf("%w (rollback incomplete, left behind: %s)", err, strings.Join(left, "; "))
				}
			}
			return se
		}
		ran = append(ran, s.name)
	}
	return nil
}
