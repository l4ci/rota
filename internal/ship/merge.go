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
// changes. Verdict's error passes through unchanged; Approve returns its
// refusal as a value, and its error is a gate that could not run.
type MergePorts struct {
	Git      Git
	Recover  land.Git // optional recovery runner, independent of caller cancellation
	Verdict  func(branch string) error
	Approve  func() (*exitcode.Refusal, error)
	Checkout Checkout // where the merge runs; scopes worktree cleanup
}

// MergeBranch merges branch into base with --no-ff and deletes it, returning
// the merge commit's short hash. A refused Approve returns its refusal and
// nothing changes. Failed merges attempt an abort; recovery
// failures pass through with their original merge error.
func MergeBranch(p MergePorts, branch, base, msg string) (string, *exitcode.Refusal, error) {
	if base == branch {
		return "", nil, &Refusal{By: blockedby.BaseBranch, Msg: "'" + branch + "' is the base branch"}
	}
	// The commit the gates judge is the commit that lands: a push to the
	// branch after this point cannot ride in.
	pin, err := p.Git.Run("rev-parse", "--verify", "-q", branch+"^{commit}")
	if err != nil {
		return "", nil, err
	}
	if pin.ExitCode != 0 {
		return "", nil, &GitError{Msg: "git rev-parse " + branch + ": " + strutil.FirstLine(pin.Stderr)}
	}
	if err := p.Verdict(branch); err != nil {
		return "", nil, err
	}
	if ref, err := p.Approve(); ref != nil || err != nil {
		return "", ref, err
	}
	if err := ClearWorktree(p.Git, branch, p.Checkout); err != nil {
		return "", nil, err
	}
	co, err := p.Git.Run("checkout", "-q", base)
	if err != nil {
		return "", nil, err
	}
	if co.ExitCode != 0 {
		return "", nil, &GitError{Msg: "git checkout " + base + ": " + strutil.FirstLine(co.Stderr)}
	}
	recover := p.Recover
	if recover == nil {
		recover = p.Git.Run
	}
	switch err := land.MergeLocal(p.Git.Run, line(pin.Stdout), msg, recover); {
	case err == nil:
	case errors.As(err, new(*land.CleanupError)):
		return "", nil, err
	case errors.As(err, new(*land.ConflictError)):
		return "", nil, &Refusal{By: blockedby.Conflict, Msg: "merge conflict; merge aborted"}
	default:
		var me *land.MergeError
		if errors.As(err, &me) {
			return "", nil, &GitError{Msg: "git merge " + branch + ": " + strutil.FirstLine(me.Out)}
		}
		return "", nil, err
	}
	del, err := p.Git.Run("branch", "-d", branch)
	if err != nil {
		return "", nil, err
	}
	if del.ExitCode != 0 {
		return "", nil, &GitError{Msg: "git branch -d " + branch + ": " + strutil.FirstLine(del.Stderr)}
	}
	sha, err := p.Git.Run("log", "-1", "--format=%h")
	if err != nil {
		return "", nil, err
	}
	return line(sha.Stdout), nil, nil
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
	// Refusal is the approver's refusal: nothing merged.
	Refusal *exitcode.Refusal
}

// MergePR merges PR pr through the gated issue backend. A refused merge is a
// *land.MergeRefusedError and a proof failure an *UnprovenError; the approver's refusal
// comes back in MergedPR.Refusal. Any other error passes through unchanged.
func MergePR(be PRMerger, pr int, items []string, approve backlog.MergeApprover) (MergedPR, error) {
	res, err := be.MergePRGated(pr, items, approve, landForge)
	switch {
	case err != nil:
		return MergedPR{}, err
	case res.Refusal != nil:
		return MergedPR{Refusal: res.Refusal}, nil
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
