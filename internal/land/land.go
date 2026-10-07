// Package land owns "land this commit on base": the two adapters that put a
// verified commit on a branch, local git and the forge, and the rules they
// share. A merge is always pinned to a commit the caller verified, so a push
// after the check cannot land unreviewed, and a failed local merge gets one abort
// attempt here. Gates (verdict, approval, proof, provenance, freshness) stay with
// the callers, who run them before landing; Policy names which path runs which.
package land

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/tracker"
)

// ErrUnpinned is a merge asked for without the commit it was verified at.
var ErrUnpinned = errors.New("land: no commit to pin the merge to")

// Git runs git in the tree being merged into.
type Git func(args ...string) (git.Result, error)

// ConflictError is a local merge that conflicted. Aborted reports whether
// git confirmed the abort succeeded.
type ConflictError struct {
	Out     string
	Aborted bool
}

func (e *ConflictError) Error() string {
	if e.Aborted {
		return "merge conflict; merge aborted"
	}
	return "merge conflict: " + e.Out
}

// MergeError is a local merge that failed for a reason other than a conflict.
// Out is git's own words.
type MergeError struct {
	Code int
	Out  string
}

func (e *MergeError) Error() string { return "git merge failed: " + e.Out }

// CleanupError retains both failures when recovery could not be confirmed.
// Callers must handle it before classifying the underlying merge failure.
type CleanupError struct {
	Merge   error
	Cleanup error
}

func (e *CleanupError) Error() string {
	return fmt.Sprintf("%v; %v; recovery unconfirmed: inspect 'git status' and retry 'git merge --abort' in the merge worktree", e.Merge, e.Cleanup)
}

func (e *CleanupError) Unwrap() []error { return []error{e.Merge, e.Cleanup} }

// RecoveryGit runs recovery in the same tree with a fresh, bounded context,
// even when the merge's caller was cancelled. It never resets or discards files.
func RecoveryGit(ctx context.Context, run git.Runner, dir string) Git {
	return func(args ...string) (git.Result, error) {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), git.Timeout)
		defer cancel()
		return run(cleanupCtx, dir, args...)
	}
}

// MergeLocal merges commit into the current branch of run's tree with --no-ff.
// The commit is the pin: it is what the caller verified, not a branch name
// that can move. A failed or interrupted merge gets one abort attempt through
// recover, which must remain usable after run is cancelled (see RecoveryGit).
// A failed abort is a *CleanupError wrapping both failures.
func MergeLocal(run Git, commit, msg string, recover Git) error {
	if commit == "" {
		return ErrUnpinned
	}
	res, err := run("merge", "--no-ff", "-m", msg, commit)
	if err == nil && res.ExitCode == 0 {
		return nil
	}
	out := res.Stdout + res.Stderr
	var conflict *ConflictError
	if err == nil {
		if git.IsMergeConflict(out) {
			conflict = &ConflictError{Out: out}
			err = conflict
		} else {
			err = &MergeError{Code: res.ExitCode, Out: out}
		}
	} else if out != "" {
		err = errors.Join(err, &MergeError{Code: res.ExitCode, Out: out})
	}
	abort, abortErr := recover("merge", "--abort")
	if abortErr != nil || abort.ExitCode != 0 {
		detail := fmt.Sprintf("git merge --abort failed (exit %d): %s", abort.ExitCode, strings.TrimSpace(abort.Stdout+abort.Stderr))
		if abortErr != nil {
			abortErr = fmt.Errorf("%s: %w", detail, abortErr)
		} else {
			abortErr = errors.New(detail)
		}
		return &CleanupError{Merge: err, Cleanup: abortErr}
	}
	if conflict != nil {
		conflict.Aborted = true
	}
	return err
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
