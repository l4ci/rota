package tracker

import "errors"

// errNoMilestones identifies adapters without milestones, independently of
// their provider-specific explanation and tracker failure kind.
var errNoMilestones = errors.New("tracker has no milestones")

// noMilestonesError preserves the adapter's *Error for errors.As callers and
// its message, while wrapping the shared refusal for errors.Is callers.
type noMilestonesError struct{ failure *Error }

func (e *noMilestonesError) Error() string { return e.failure.Error() }

func (e *noMilestonesError) Unwrap() []error {
	return []error{e.failure, errNoMilestones}
}

// IsNoMilestones reports whether err is a tracker refusing a milestone call
// because it has no milestones at all, as opposed to a failing call.
func IsNoMilestones(err error) bool { return errors.Is(err, errNoMilestones) }
