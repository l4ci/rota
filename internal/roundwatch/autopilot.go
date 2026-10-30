package roundwatch

import (
	"context"
	"slices"
	"time"

	"github.com/l4ci/rota/internal/roundtick"
)

// ReasonStopped: the tick found no round to work for.
const ReasonStopped = "stopped"

// AutopilotOpts wires a watch to the autopilot tick.
type AutopilotOpts struct {
	Tick      func() (roundtick.Result, error)
	Stopped   func(error) bool // Tick's error means the round is gone
	TrainFill bool             // a completed merge wakes the orchestrator
	Poll      time.Duration    // pause after a wake Run reports but the tick does not
}

// AutopilotResult is a watch result plus the last tick. TickErr is set when
// the tick failed; Tick is nil then and after a stop.
type AutopilotResult struct {
	Result
	Tick    *roundtick.Result
	TickErr error
}

// RunAutopilot is `round watch --autopilot`: tick first, then wait, tick on
// every wake, and return for new attention, a completed merge (under train
// fill), a stop, a failed tick or the heartbeat. Fill progress stays quiet.
func RunAutopilot(ctx context.Context, env Env, o Opts, a AutopilotOpts) (AutopilotResult, error) {
	deadline := env.Now().Add(o.Heartbeat)
	for {
		tr, err := a.Tick()
		left := deadline.Sub(env.Now())
		switch {
		case err != nil && a.Stopped != nil && a.Stopped(err):
			return AutopilotResult{Result: Result{Reason: ReasonStopped}}, nil
		case err != nil:
			return AutopilotResult{Result: Result{Reason: ReasonChange}, TickErr: err}, nil
		case ctx.Err() != nil:
			return AutopilotResult{Result: Result{Reason: ReasonInterrupt}, Tick: &tr}, nil
		case wakes(tr, a.TrainFill):
			return AutopilotResult{Result: Result{Reason: ReasonChange}, Tick: &tr}, nil
		case left <= 0:
			return AutopilotResult{Result: Result{Reason: ReasonHeartbeat}, Tick: &tr}, nil
		}
		wo := o
		wo.Heartbeat = left
		res, err := Run(ctx, env, wo)
		if err != nil {
			return AutopilotResult{}, err
		}
		if res.Reason == ReasonHeartbeat || res.Reason == ReasonInterrupt {
			// Tick once more so the report is current; a failed tick is
			// dropped here as it always was.
			if t2, err := a.Tick(); err == nil {
				tr = t2
			}
			return AutopilotResult{Result: res, Tick: &tr}, nil
		}
		// A slot the orchestrator already knows about wakes Run at once: the
		// pause keeps the loop from spinning on it.
		env.Sleep(ctx, a.Poll)
	}
}

// wakes is the autopilot's wake rule: a new attention item, or a completed
// merge when train fill is on.
func wakes(tr roundtick.Result, trainFill bool) bool {
	return len(tr.New) > 0 || trainFill && slices.ContainsFunc(tr.Did, func(a roundtick.Action) bool { return a.Action == "merge" })
}
