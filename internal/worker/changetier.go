package worker

import (
	"context"
	"fmt"

	"github.com/l4ci/rota/internal/change"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/tracker"
)

// ChangeSources is where a diff's change tier is read from (#2348). The verb,
// the review depth, the gate and the train all classify through ClassifyChange,
// so they agree on the tier and its reason.
type ChangeSources struct {
	// Policy is change.*; PolicyErr is the error of a malformed one.
	Policy    change.Policy
	PolicyErr error
	Git       git.Runner
	Dir       string
	Span      string // base...head
	// PRBody reads the PR description for its Door line; nil when there is no PR.
	PRBody func() (string, error)
}

// ClassifyChange reads the diff and the PR body and puts the change in a tier.
// It fails closed: a malformed policy, a diff it cannot read or a PR body it
// cannot read (other than no forge CLI) gives full with a reason saying so,
// never a cheaper tier and never an error.
func ClassifyChange(ctx context.Context, s ChangeSources) change.Result {
	full := func(lines int, why string) change.Result {
		return change.Result{Tier: change.TierFull, Reason: why, Lines: lines}
	}
	if s.PolicyErr != nil {
		return full(-1, fmt.Sprintf("%v; run rota config check", s.PolicyErr))
	}
	files, err := git.DiffFiles(ctx, s.Git, s.Dir, s.Span)
	if err != nil {
		return full(-1, fmt.Sprintf("could not list the files of %s: %v", s.Span, err))
	}
	lines, err := git.ChangedLines(ctx, s.Git, s.Dir, s.Span)
	if err != nil {
		return full(-1, fmt.Sprintf("could not count the changed lines of %s: %v", s.Span, err))
	}
	in := change.Input{Files: files, Lines: lines}
	res := change.Classify(in, s.Policy)
	// Only a Door line can lift a cheaper tier, so a full one skips the read.
	if res.Tier == change.TierFull || s.PRBody == nil {
		return res
	}
	body, err := s.PRBody()
	if err != nil && !tracker.IsCLIMissing(err) {
		return full(lines, fmt.Sprintf("could not read the PR body for its Door line: %v", err))
	}
	in.Door = config.DoorOneWay(body)
	return change.Classify(in, s.Policy)
}
