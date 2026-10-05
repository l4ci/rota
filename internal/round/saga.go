package round

// step is one unit of a saga: a change plus the compensation that takes it
// back. Assign and Transfer are ordered lists of steps run by runSteps, so each
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
	undo func()
	// keep makes a failure of this step leave the earlier steps in place: the
	// step's effect may already be out in the world (a pane that holds the
	// brief), so rolling back would strand it. The failed step cleans up after
	// itself inside do.
	keep bool
}

// runSteps runs steps in order and returns the first error. A failed step is
// not undone (it leaves nothing behind itself); unless it is keep, the steps
// before it are undone, latest first.
func runSteps(steps []step) error {
	for i, s := range steps {
		if s.skip != nil && s.skip() {
			continue
		}
		if err := s.do(); err != nil {
			if !s.keep {
				for j := i - 1; j >= 0; j-- {
					if u := steps[j].undo; u != nil {
						u()
					}
				}
			}
			return err
		}
	}
	return nil
}
