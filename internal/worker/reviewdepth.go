package worker

import (
	"context"
	"fmt"
	"slices"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/tracker"
)

// ReviewSources is where a branch's review depth is read from. The worker gate
// and `rota review depth` (so /rota-ship) both resolve through ResolveReviewDepth,
// so they pick the same depth and print the same reason.
type ReviewSources struct {
	Policy config.ReviewPolicy
	Git    git.Runner
	Dir    string
	Span   string   // base...head
	Labels []string // labels already known, added to the issue's own
	// Issue and Issues read the branch's issue for its labels and its declared
	// files. Issues is nil for a file source, which has none.
	Issue  tracker.IssueKey
	Issues interface {
		Get(ctx context.Context, key tracker.IssueKey, withComments bool) (tracker.Issue, error)
	}
	// PRBody reads the PR description for its Door line; nil when there is no PR.
	PRBody func() (string, error)
}

// ReviewDepth is a resolved depth with the inputs it was read from.
type ReviewDepth struct {
	config.ReviewDecision
	Changed int
	Labels  []string
}

// ReviewDepthError says which read failed. Guessing a depth could pass a risky
// branch as light, so every failed read is an error.
type ReviewDepthError struct {
	Stage string // "diff", "issue" or "pr"
	Msg   string
	Err   error
}

func (e *ReviewDepthError) Error() string { return e.Msg }
func (e *ReviewDepthError) Unwrap() error { return e.Err }

// ResolveReviewDepth reads the diff, the issue and the PR body and applies the
// policy. The issue is read only when the policy can use it: for a label to
// match or for a declared file set to drift from.
func ResolveReviewDepth(ctx context.Context, s ReviewSources) (ReviewDepth, error) {
	changed, err := git.ChangedLines(ctx, s.Git, s.Dir, s.Span)
	if err != nil {
		return ReviewDepth{}, &ReviewDepthError{"diff", fmt.Sprintf("could not diff %s to pick the review depth", s.Span), err}
	}
	files, err := git.ChangedPaths(ctx, s.Git, s.Dir, s.Span)
	if err != nil {
		return ReviewDepth{}, &ReviewDepthError{"diff", fmt.Sprintf("could not list the files of %s to pick the review depth", s.Span), err}
	}
	in := config.ReviewInput{Changed: changed, Files: files, Labels: slices.Clone(s.Labels)}
	if s.Issues != nil && s.Issue != "" && s.Policy.NeedsIssue() {
		is, err := s.Issues.Get(ctx, s.Issue, false)
		if err != nil {
			return ReviewDepth{}, &ReviewDepthError{"issue", fmt.Sprintf("could not read %s for its labels and files to pick the review depth: %v", s.Issue.Ref(), err), err}
		}
		for _, l := range is.Labels {
			if !slices.Contains(in.Labels, l) {
				in.Labels = append(in.Labels, l)
			}
		}
		in.IssueBody = is.Body
	}
	if s.PRBody != nil && s.Policy.MayBeLight() {
		body, err := s.PRBody()
		if err != nil && !tracker.IsCLIMissing(err) {
			return ReviewDepth{}, &ReviewDepthError{"pr", fmt.Sprintf("could not read the PR body for its Door line to pick the review depth: %v", err), err}
		}
		in.PRBody = body
	}
	return ReviewDepth{s.Policy.Decide(in), changed, in.Labels}, nil
}
