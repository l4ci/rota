package ship

import (
	"context"
	"errors"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/land"
	"github.com/l4ci/rota/internal/strutil"
)

// MergePorts are what MergeBranch leaves to its caller. Verdict is the B3
// gate and Approve the merge-approval gate (B1); both run before anything
// changes and receive the pinned landing commit. Verdict also receives the
// branch for store lookup and messages. Verdict's error passes through unchanged;
// Approve returns its refusal as a value, which MergeBranch turns into a
// *GateRefusedError; its error is a gate that could not run.
type MergePorts struct {
	Git      Git
	Recover  land.Git // optional recovery runner, independent of caller cancellation
	Verdict  func(branch, head string) error
	Approve  func(head string) (*exitcode.Refusal, error)
	Checkout Checkout // where the merge runs; scopes worktree cleanup
}

// BranchMovedError and PostMergeError preserve the ship API while local
// landing owns the pin and its post-merge read-back.
type BranchMovedError = land.BranchMovedError
type PostMergeError = land.PostMergeError

// MergeBranch merges branch into base with --no-ff and deletes it if its tip
// still matches the pin, returning the merge commit's short hash. A moved tip
// returns that hash and a BranchMovedError. A refused Approve returns a
// *GateRefusedError and nothing changes. Failed merges attempt an abort; recovery
// failures pass through with their original merge error.
func MergeBranch(p MergePorts, branch, base, msg string) (string, error) {
	if base == branch {
		return "", &Refusal{By: blockedby.BaseBranch, Msg: "'" + branch + "' is the base branch"}
	}
	sha, err := land.LandLocal(land.LocalPorts{
		Git: p.Git.Run, Recover: p.Recover,
		Verify: func(head string) error {
			if err := p.Verdict(branch, head); err != nil {
				return err
			}
			if ref, err := p.Approve(head); ref != nil {
				return &GateRefusedError{Ref: ref}
			} else {
				return err
			}
		},
		Prepare: func() error { return ClearWorktree(p.Git, branch, p.Checkout) },
	}, land.LocalRequest{Branch: branch, Base: base, Message: msg, Checkout: base, DeleteBranch: true, ShortSHA: true})
	if err != nil {
		f := land.Classify(err)
		msg, hint := f.Refusal(mergeWords(branch))
		switch f.Kind {
		case land.FailConflict:
			return "", &Refusal{By: blockedby.Conflict, Msg: msg, Hint: hint}
		case land.FailMerge:
			return "", &GitError{Msg: msg}
		}
	}
	return sha, err
}

// mergeWords is how ship words a failed merge of branch: land owns the rule,
// ship only names the branch.
func mergeWords(branch string) land.Words {
	return land.Words{
		Failed:   "git merge " + branch,
		Conflict: "merge of " + branch + " conflicted in {paths}; merge aborted",
	}
}

// ChangedFiles lists the files a merge range changes, for the merge-approval
// gate.
func ChangedFiles(g Git, rng string) ([]string, error) {
	run := func(_ context.Context, _ string, args ...string) (git.Result, error) { return g.Run(args...) }
	paths, err := git.ChangedPaths(context.Background(), run, "", rng)
	var ee *git.ExitError
	if errors.As(err, &ee) {
		return nil, &GitError{Msg: "git diff " + rng + ": " + strutil.FirstLine(ee.Stderr)}
	}
	return paths, err
}

// PRMerger is the issue backend's gated PR merge.
type PRMerger interface {
	MergePRGated(pr int, items []string, approve backlog.MergeApprover, lander backlog.Lander) (backlog.MergeResult, error)
}

// UnprovenError is a PR held back because linked items have no proof.
type UnprovenError struct{ IDs []string }

func (e *UnprovenError) Error() string { return "an item has no proof; not merged" }

// MergedPR is a merged PR: the short merge hash and the items it closed.
type MergedPR struct {
	SHA    string
	Closed []backlog.ItemRef
}

// MergePR merges PR pr through the gated issue backend. A refused merge is a
// *land.MergeRefusedError, a proof failure an *UnprovenError and the approver's
// refusal a *GateRefusedError. Any other error passes through unchanged.
func MergePR(be PRMerger, pr int, items []string, approve backlog.MergeApprover) (MergedPR, error) {
	res, err := be.MergePRGated(pr, items, approve, landForge)
	switch {
	case err != nil:
		return MergedPR{}, err
	case res.Refusal != nil:
		return MergedPR{}, &GateRefusedError{Ref: res.Refusal}
	case len(res.Unproven) > 0:
		ids := []string{}
		for _, u := range res.Unproven {
			ids = append(ids, u.ID)
		}
		return MergedPR{}, &UnprovenError{IDs: ids}
	}
	return MergedPR{SHA: res.SHA[:min(7, len(res.SHA))], Closed: res.Closed}, nil
}

// landForge lands the PR pinned to its verified head and deletes the branch.
func landForge(ctx context.Context, pt backlog.PRTracker, pr int, head string) (string, error) {
	return land.MergeForge(ctx, pt, pr, head, true)
}
