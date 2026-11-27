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
	TrainFill bool          // a completed merge wakes the orchestrator
	Poll      time.Duration // pause after a wake Run reports but the tick does not
}

// AutopilotResult is a watch result plus the last tick. TickErr is set when
// the tick failed; Tick is nil then and after a stop at tick entry. StopErr is
// the classified stop (roundtick.ErrStopped, ErrBaseRed or ErrNoLease). A stop
// seen mid-tick keeps the tick (Tick.Stopped is set), so its merges are reported.
type AutopilotResult struct {
	Result
	Tick    *roundtick.Result
	TickErr error
	StopErr error // set with ReasonStopped: the tick error, or Tick.Stopped
}

// RunAutopilot is `round watch --autopilot`: tick first, then wait, tick on
// every wake, and return for new attention, a completed merge (under train
// fill), a stop, a failed tick or the heartbeat. Fill progress stays quiet.
//
// One switch classifies every tick, the closing one after the heartbeat
// included. An interrupted watch runs no closing tick: a tick can merge and
// assign, and its context is already cancelled.
func RunAutopilot(ctx context.Context, env Env, o Opts, a AutopilotOpts) (AutopilotResult, error) {
	deadline := env.Now().Add(o.Heartbeat)
	var waited time.Duration // set when the next tick is the closing one
	for {
		tr, err := a.Tick()
		left := deadline.Sub(env.Now())
		switch {
		case err != nil && roundtick.IsStopped(err):
			return AutopilotResult{Result: Result{Reason: ReasonStopped, Waited: waited}, StopErr: err}, nil
		case err != nil:
			return AutopilotResult{Result: Result{Reason: ReasonChange, Waited: waited}, TickErr: err}, nil
		case tr.Stopped != nil:
			return AutopilotResult{Result: Result{Reason: ReasonStopped, Waited: waited}, Tick: &tr, StopErr: tr.Stopped}, nil
		case ctx.Err() != nil:
			return AutopilotResult{Result: Result{Reason: ReasonInterrupt, Waited: waited}, Tick: &tr}, nil
		case wakes(tr, a.TrainFill):
			return AutopilotResult{Result: Result{Reason: ReasonChange, Waited: waited}, Tick: &tr}, nil
		case left <= 0:
			return AutopilotResult{Result: Result{Reason: ReasonHeartbeat, Waited: waited}, Tick: &tr}, nil
		}
		wo := o
		wo.Heartbeat = left
		res, err := Run(ctx, env, wo)
		if err != nil {
			return AutopilotResult{}, err
		}
		switch res.Reason {
		case ReasonInterrupt:
			return AutopilotResult{Result: res, Tick: &tr}, nil
		case ReasonHeartbeat:
			waited = res.Waited // the deadline passed: tick once more, no pause
			continue
		}
		// A slot the orchestrator already knows about wakes Run at once: the
		// pause keeps the loop from spinning on it.
		env.Sleep(ctx, a.Poll)
	}
}

// wakes is the autopilot's wake rule: a new attention item, or a completed
// merge when train fill is on.
func wakes(tr roundtick.Result, trainFill bool) bool {
	return len(tr.New) > 0 || trainFill && slices.ContainsFunc(tr.Did, func(a roundtick.Action) bool { return a.Action == roundtick.ActionMerge })
}
