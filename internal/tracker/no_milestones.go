package tracker

import (
	"context"
	"errors"
)

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

// noMilestones is the milestone half of IssueTracker for an adapter whose
// tracker has none. Embed it: the five milestone methods refuse with one
// error (message "issues.source <provider> has no milestones<detail>"), and
// FindMilestone finds nothing.
type noMilestones struct{ provider, detail string }

// noMilestonesErr is the refusal the milestone methods return, for an
// adapter's own call sites that refuse a milestone argument.
func (n noMilestones) noMilestonesErr() error {
	return &noMilestonesError{failed("issues.source %s has no milestones%s", n.provider, n.detail)}
}

func (n noMilestones) Milestones(context.Context, string) ([]Milestone, error) {
	return nil, n.noMilestonesErr()
}

// FindMilestone finds none: the tracker holds no rota milestones.
func (n noMilestones) FindMilestone(context.Context, string) (string, bool, error) {
	return "", false, nil
}

func (n noMilestones) CreateMilestone(context.Context, string, string) (int, error) {
	return 0, n.noMilestonesErr()
}

func (n noMilestones) EditMilestone(context.Context, int, MilestoneEdit) error {
	return n.noMilestonesErr()
}

func (n noMilestones) IssuesInMilestone(context.Context, string, string) ([]Issue, error) {
	return nil, n.noMilestonesErr()
}

// keyClosing is ClosesIssue for a key-based tracker: a PR closes an issue by
// naming its key in the head branch or the title, as the tracker's own
// development panel links them.
type keyClosing struct{}

func (keyClosing) ClosesIssue(key IssueKey, c ClosingSource) bool { return ClosesByKey(key, c) }
