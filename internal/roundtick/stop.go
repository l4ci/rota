package roundtick

import (
	"errors"
	"fmt"

	"github.com/l4ci/rota/internal/round"
)

// The stop reasons of the autopilot, defined once. A tick that returns one of
// these did no (further) work; the CLI and the watch classify the error with
// IsStopped and pick the wording from the reason.

// ErrStopped says the round is winding down: Run assigns and merges nothing.
var ErrStopped = errors.New("the round is stopped by `rota round wind-down`; the autopilot resumes when wind-down finishes or a wind-down verifies the base green")

// StopVerifyFailed is the stop reason of a red wind-down verify.
const StopVerifyFailed = round.StopVerifyFailed

// ErrBaseRed is ErrStopped after a wind-down verify went red: only a wind-down
// that verifies the base green resumes the autopilot.
var ErrBaseRed = fmt.Errorf("%w: the base failed the wind-down verify; fix the base, then re-run `rota round wind-down`", ErrStopped)

// ErrNoLease says the autopilot has no round to work for: the lease is gone
// (wind-down released it, or another orchestrator took it).
var ErrNoLease = errors.New("this process holds no round lease: the autopilot stops")

// IsStopped is true when the tick found no round to work for: the lease is
// gone, or the round is stopped for wind-down.
func IsStopped(err error) bool {
	return errors.Is(err, ErrNoLease) || errors.Is(err, ErrStopped)
}

// StopError turns the stored stop value (State.Stopped, State.StoppedReason)
// into the error Run returns: nil while the round runs.
func StopError(stopped bool, reason string) error {
	switch {
	case !stopped:
		return nil
	case reason == StopVerifyFailed:
		return ErrBaseRed
	}
	return ErrStopped
}

// StopPort is the round.AutopilotStop stored in the autopilot state file.
type StopPort struct{}

// Load returns the round's stored stop state.
func (StopPort) Load(commonDir string, round int) (bool, string) {
	s := LoadState(commonDir, round)
	return s.Stopped, s.StoppedReason
}

// Set stops or resumes the round's autopilot.
func (StopPort) Set(commonDir string, round int, stopped bool, reason string) error {
	return SetStop(commonDir, round, stopped, reason)
}

// Clear removes the state of a finished round.
func (StopPort) Clear(commonDir string) error { return ClearState(commonDir) }
