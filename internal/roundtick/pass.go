package roundtick

import (
	"context"
	"errors"
	"fmt"
)

// ErrObserveMismatch is a world built in a different mode than Pass.Observe
// asked for. The pass refuses to run it rather than act in the wrong mode.
var ErrObserveMismatch = errors.New("roundtick: the world's observe mode differs from the pass's")

// Pass is one autopilot pass: where its memory lives and how to reach the
// world. Observe is stated once here; the world and the saving both follow it.
type Pass struct {
	// CommonDir and Round locate the stored state of the round.
	CommonDir string
	Round     int
	// Observe runs the pass read-only: nothing is saved and the world is
	// built in observation mode.
	Observe bool
	Config  Config
	// World builds the tick's seams from the state loaded for this pass and
	// the Observe flag.
	World func(st State, observe bool) World
}

// RunPass loads the round's state, applies it to a fresh Env, runs one tick
// and saves what it left behind, unless it only observes. A failed save is
// warned about, not returned: the tick's work is already done.
func RunPass(ctx context.Context, p Pass) (Result, error) {
	state := LoadState(p.CommonDir, p.Round)
	w := p.World(state, p.Observe)
	if w.Observing() != p.Observe {
		return Result{}, ErrObserveMismatch
	}
	e := NewEnv(w, p.Config)
	state.Apply(&e)
	r, err := Run(ctx, e)
	if err != nil {
		return r, err
	}
	if !p.Observe {
		if err := SaveState(p.CommonDir, p.Round, r); err != nil {
			w.Warn(fmt.Sprintf("autopilot state not saved: %v", err))
		}
	}
	return r, nil
}
