package ship

import (
	"context"
	"errors"
	"fmt"

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

// BranchMovedError reports a landed pin whose branch must be kept because its
// current tip differs. MergeBranch returns the merge hash alongside this error.
type BranchMovedError struct{ Branch, Base, Pinned, Tip, MergeSHA string }

func (e *BranchMovedError) Error() string {
	return fmt.Sprintf("pinned commit %s landed on %s as merge %s. Branch %s moved to %s; that tip was not gated or merged by this operation; branch kept. Commits added after the pin need their own review and merge", e.Pinned, e.Base, e.MergeSHA, e.Branch, e.Tip)
}

// PostMergeError preserves the landed commit when later Git work fails.
// MergeSHA is empty only when both the normal and recovery reads failed.
type PostMergeError struct {
	Pinned, Base, MergeSHA string
	Err                    error
}

func (e *PostMergeError) Error() string {
	landing := fmt.Sprintf("pinned commit %s landed on %s", e.Pinned, e.Base)
	if e.MergeSHA != "" {
		landing += " as merge " + e.MergeSHA
	} else {
		landing += "; merge SHA could not be read"
	}
	return landing + ". Post-merge cleanup failed: " + e.Err.Error() + ". Do not retry the merge"
}

func (e *PostMergeError) Unwrap() error { return e.Err }

// MergeBranch merges branch into base with --no-ff and deletes it if its tip
// still matches the pin, returning the merge commit's short hash. A moved tip
// returns that hash and a BranchMovedError. A refused Approve returns a
// *GateRefusedError and nothing changes. Failed merges attempt an abort; recovery
// failures pass through with their original merge error.
func MergeBranch(p MergePorts, branch, base, msg string) (string, error) {
	if base == branch {
		return "", &Refusal{By: blockedby.BaseBranch, Msg: "'" + branch + "' is the base branch"}
	}
	// The commit the gates judge is the commit that lands: a push to the
	// branch after this point cannot ride in.
	pin, err := p.Git.Run("rev-parse", "--verify", "-q", branch+"^{commit}")
	if err != nil {
		return "", err
	}
	if pin.ExitCode != 0 {
		return "", &GitError{Msg: "git rev-parse " + branch + ": " + strutil.FirstLine(pin.Stderr)}
	}
	head := line(pin.Stdout)
	if err := p.Verdict(branch, head); err != nil {
		return "", err
	}
	if ref, err := p.Approve(head); ref != nil {
		return "", &GateRefusedError{Ref: ref}
	} else if err != nil {
		return "", err
	}
	if err := ClearWorktree(p.Git, branch, p.Checkout); err != nil {
		return "", err
	}
	co, err := p.Git.Run("checkout", "-q", base)
	if err != nil {
		return "", err
	}
	if co.ExitCode != 0 {
		return "", &GitError{Msg: "git checkout " + base + ": " + strutil.FirstLine(co.Stderr)}
	}
	recover := p.Recover
	if recover == nil {
		recover = p.Git.Run
	}
	switch err := land.MergeLocal(p.Git.Run, head, msg, recover); {
	case err == nil:
	case errors.As(err, new(*land.CleanupError)):
		return "", err
	case errors.As(err, new(*land.ConflictError)):
		return "", &Refusal{By: blockedby.Conflict, Msg: "merge conflict; merge aborted"}
	default:
		var me *land.MergeError
		if errors.As(err, &me) {
			return "", &GitError{Msg: "git merge " + branch + ": " + strutil.FirstLine(me.Out)}
		}
		return "", err
	}
	postMerge := func(sha string, err error) error {
		return &PostMergeError{Pinned: head, Base: base, MergeSHA: sha, Err: err}
	}
	sha, err := p.Git.Run("log", "-1", "--format=%h")
	if err != nil || sha.ExitCode != 0 || line(sha.Stdout) == "" {
		if err == nil {
			err = &GitError{Msg: "git log -1 --format=%h: " + strutil.FirstLine(sha.Stderr)}
		}
		// The caller's context may have been cancelled after the merge. Read
		// the base through the bounded recovery runner before reporting it.
		read, readErr := recover("rev-parse", "--short", base)
		if readErr != nil {
			err = errors.Join(err, readErr)
		} else if read.ExitCode == 0 && line(read.Stdout) != "" {
			return line(read.Stdout), postMerge(line(read.Stdout), err)
		} else {
			err = errors.Join(err, &GitError{Msg: "git rev-parse --short " + base + ": " + strutil.FirstLine(read.Stderr)})
		}
		return "", postMerge("", err)
	}
	mergedSHA := line(sha.Stdout)
	tip, err := p.Git.Run("rev-parse", "--verify", "-q", branch+"^{commit}")
	if err != nil {
		return mergedSHA, postMerge(mergedSHA, err)
	}
	if tip.ExitCode != 0 {
		return mergedSHA, postMerge(mergedSHA, &GitError{Msg: "git rev-parse " + branch + ": " + strutil.FirstLine(tip.Stderr)})
	}
	if current := line(tip.Stdout); current != head {
		return mergedSHA, &BranchMovedError{Branch: branch, Base: base, Pinned: head, Tip: current, MergeSHA: mergedSHA}
	}
	del, err := p.Git.Run("branch", "-d", branch)
	if err != nil {
		return mergedSHA, postMerge(mergedSHA, err)
	}
	if del.ExitCode != 0 {
		return mergedSHA, postMerge(mergedSHA, &GitError{Msg: "git branch -d " + branch + ": " + strutil.FirstLine(del.Stderr)})
	}
	return mergedSHA, nil
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
