// Package land owns "land this commit on base": the two adapters that put a
// verified commit on a branch, local git and the forge, and the rules they
// share. A merge is always pinned to a commit the caller verified, so a push
// after the check cannot land unreviewed, and a failed local merge is aborted
// here once. Gates (verdict, approval, proof, provenance, freshness) stay with
// the callers, who run them before landing.
package land

import (
	"context"
	"errors"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/tracker"
)

// ErrUnpinned is a merge asked for without the commit it was verified at.
var ErrUnpinned = errors.New("land: no commit to pin the merge to")

// Git runs git in the tree being merged into.
type Git func(args ...string) (git.Result, error)

// ConflictError is a local merge that conflicted. It has been aborted: the
// tree is as it was.
type ConflictError struct{ Out string }

func (e *ConflictError) Error() string { return "merge conflict; merge aborted" }

// MergeError is a local merge that failed for a reason other than a conflict
// (no committer identity, a hook, a locked index). Out is git's own words.
// The merge has been aborted.
type MergeError struct {
	Code int
	Out  string
}

func (e *MergeError) Error() string { return "git merge failed: " + e.Out }

// MergeLocal merges commit into the current branch of run's tree with --no-ff.
// The commit is the pin: it is what the caller verified, not a branch name
// that can move. A failed merge is aborted; a conflict is a *ConflictError and
// anything else a *MergeError (or the runner's own error).
func MergeLocal(run Git, commit, msg string) error {
	if commit == "" {
		return ErrUnpinned
	}
	res, err := run("merge", "--no-ff", "-m", msg, commit)
	if err != nil {
		return err
	}
	if res.ExitCode == 0 {
		return nil
	}
	run("merge", "--abort")
	out := res.Stdout + res.Stderr
	if git.IsMergeConflict(out) {
		return &ConflictError{Out: out}
	}
	return &MergeError{Code: res.ExitCode, Out: out}
}

// Requester is the tracker's merge request; Merger its confirmed merge.
type (
	Requester interface {
		PRRequestMerge(ctx context.Context, pr int, o tracker.MergeOpts) error
	}
	Merger interface {
		PRMerge(ctx context.Context, pr int, o tracker.MergeOpts) (string, error)
	}
)

// RequestForge asks the forge to merge PR pr pinned to head, the commit the
// caller verified; the forge refuses it when the PR head has moved. It does
// not confirm the merge landed.
func RequestForge(ctx context.Context, f Requester, pr int, head string, deleteBranch bool) error {
	if head == "" {
		return ErrUnpinned
	}
	return f.PRRequestMerge(ctx, pr, tracker.MergeOpts{HeadSHA: head, DeleteBranch: deleteBranch})
}

// MergeForge is RequestForge plus the confirmation: it returns the merge
// commit sha, or fails when nothing landed.
func MergeForge(ctx context.Context, f Merger, pr int, head string, deleteBranch bool) (string, error) {
	if head == "" {
		return "", ErrUnpinned
	}
	return f.PRMerge(ctx, pr, tracker.MergeOpts{HeadSHA: head, DeleteBranch: deleteBranch})
}
