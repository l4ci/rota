package worker

import "github.com/l4ci/rota/internal/landing"

import "fmt"

// landingStageResult carries either a typed handoff to the next stage or the
// terminal public answer. An error skips all remaining stages too.
type landingStageResult[V, R any] struct {
	Value  V
	Result R
	Done   bool
	Err    error
}

// runLandingStage is the common runner for gate and train. A stopped run keeps
// its answer even when the next stage expects a different input type.
func runLandingStage[I, O, R any](previous landingStageResult[I, R], stage func(I) (O, R, bool, error)) landingStageResult[O, R] {
	if previous.Done || previous.Err != nil {
		return landingStageResult[O, R]{Result: previous.Result, Done: previous.Done, Err: previous.Err}
	}
	value, result, done, err := stage(previous.Value)
	return landingStageResult[O, R]{Value: value, Result: result, Done: done, Err: err}
}

// runGateStage wires the gateRun steps: the public answer travels independently
// from the named stage outputs captured by the caller. The shared landing runner owns short-circuiting.
func runGateStage[P, O any](previous landingStageResult[P, landing.GateResult], stage func(landing.GateResult) (O, landing.GateResult, bool, error)) landingStageResult[O, landing.GateResult] {
	return runLandingStage(previous, func(P) (O, landing.GateResult, bool, error) {
		return stage(previous.Result)
	})
}

func checkBrokeMessage(subject, msg string) string {
	return fmt.Sprintf("CHECK-BROKE %s — %s", subject, msg)
}
