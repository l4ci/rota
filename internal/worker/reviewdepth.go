package worker

import (
	"context"
	"fmt"
	"slices"
	"strings"

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
	Issue tracker.IssueKey
	// Members are the other issues of a bundled slot (Issue is the lead). Each is
	// read like the lead and the highest depth any of them requires wins.
	Members []tracker.IssueKey
	Issues  interface {
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
	// --no-renames lists both the old and the new path of a move, so a file
	// moved out of a protected directory still counts as touching it. -z keeps
	// git from C-quoting unusual names, which no glob would match.
	res, err := s.Git(ctx, s.Dir, "diff", "--name-only", "-z", "--no-renames", s.Span)
	if err == nil && res.ExitCode != 0 {
		err = &git.ExitError{Args: "diff --name-only " + s.Span, ExitCode: res.ExitCode, Stderr: res.Stderr}
	}
	var files []string
	for _, l := range strings.Split(res.Stdout, "\x00") {
		if l != "" {
			files = append(files, l)
		}
	}
	if err != nil {
		return ReviewDepth{}, &ReviewDepthError{"diff", fmt.Sprintf("could not list the files of %s to pick the review depth", s.Span), err}
	}
	in := config.ReviewInput{Changed: changed, Files: files, Labels: slices.Clone(s.Labels)}
	decide := s.Policy.Decide
	if s.Issues != nil && s.Issue != "" && s.Policy.NeedsIssue() {
		keys := append([]tracker.IssueKey{s.Issue}, s.Members...)
		var got []tracker.Issue
		var bodies []string
		for _, key := range keys {
			is, err := s.Issues.Get(ctx, key, false)
			if err != nil {
				return ReviewDepth{}, &ReviewDepthError{"issue", fmt.Sprintf("could not read %s for its labels and files to pick the review depth: %v", key.Ref(), err), err}
			}
			got = append(got, is)
			bodies = append(bodies, is.Body)
			for _, l := range is.Labels {
				if !slices.Contains(in.Labels, l) {
					in.Labels = append(in.Labels, l)
				}
			}
		}
		in.IssueBody = strings.Join(bodies, "\n\n")
		if len(got) > 1 {
			// One decision per bundled issue, the highest wins: a union of
			// labels would let a `none` on one issue beat another's default.
			// The PR body is read below, once, for every decision.
			decide = func(in config.ReviewInput) config.ReviewDecision {
				var best config.ReviewDecision
				for i, is := range got {
					one := in
					one.Labels = slices.Clone(s.Labels)
					for _, l := range is.Labels {
						if !slices.Contains(one.Labels, l) {
							one.Labels = append(one.Labels, l)
						}
					}
					if d := s.Policy.Decide(one); i == 0 || d.Depth.Outranks(best.Depth) {
						best = d
					}
				}
				return best
			}
		}
	}
	if s.PRBody != nil && s.Policy.MayBeBelowFull() {
		body, err := s.PRBody()
		if err != nil && !tracker.IsCLIMissing(err) {
			return ReviewDepth{}, &ReviewDepthError{"pr", fmt.Sprintf("could not read the PR body for its Door line to pick the review depth: %v", err), err}
		}
		in.PRBody = body
	}
	return ReviewDepth{decide(in), changed, in.Labels}, nil
}

// DoorNote explains why a depth below full has not seen the PR's Door line: there
// was no PR to read, or no forge to read one from. It is empty when the line was
// read, when the depth is full, when a label mapped to none (final, so the line
// cannot change it) or when the policy cannot come out below full.
func DoorNote(policy config.ReviewPolicy, res ReviewDepth, prFound, noForge bool) string {
	labelNone := res.Signal == config.SignalLabel && res.Depth == config.DepthNone
	if prFound || res.Depth == config.DepthFull || labelNone || !policy.MayBeBelowFull() {
		return ""
	}
	if noForge {
		return "; no forge to read, so no Door line was read"
	}
	return "; no PR yet, so no Door line was read"
}
