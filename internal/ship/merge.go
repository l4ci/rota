package ship

import (
	"errors"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/pystr"
)

// MergePorts are what MergeBranch leaves to its caller. Verdict is the B3
// gate and Approve the merge-approval gate (B1); both run before anything
// changes and their errors pass through unchanged.
type MergePorts struct {
	Git     Git
	Verdict func(branch string) error
	Approve func() error
	OnDisk  func() string
}

// MergeBranch merges branch into base with --no-ff and deletes it, returning
// the merge commit's short hash. A conflict aborts the merge and leaves the
// tree as it was.
func MergeBranch(p MergePorts, branch, base, msg string) (string, error) {
	if base == branch {
		return "", &Refusal{By: "base branch", Msg: "'" + branch + "' is the base branch"}
	}
	if err := p.Verdict(branch); err != nil {
		return "", err
	}
	if err := p.Approve(); err != nil {
		return "", err
	}
	if err := ClearWorktree(p.Git, branch, p.OnDisk); err != nil {
		return "", err
	}
	co, err := p.Git.Run("checkout", "-q", base)
	if err != nil {
		return "", err
	}
	if co.ExitCode != 0 {
		return "", &GitError{Msg: "git checkout " + base + ": " + firstLine(co.Stderr)}
	}
	// A local merge: there is no forge PR head to pin, the branch tip is the
	// thing merged. Merges that go through a forge pin via tracker.MergeOpts.
	mg, err := p.Git.Run("merge", "--no-ff", branch, "-m", msg)
	if err != nil {
		return "", err
	}
	if mg.ExitCode != 0 {
		if git.IsMergeConflict(mg.Stdout + mg.Stderr) {
			p.Git.Run("merge", "--abort")
			return "", &Refusal{By: "conflict", Msg: "merge conflict; merge aborted"}
		}
		return "", &GitError{Msg: "git merge " + branch + ": " + firstLine(mg.Stderr+mg.Stdout)}
	}
	del, err := p.Git.Run("branch", "-d", branch)
	if err != nil {
		return "", err
	}
	if del.ExitCode != 0 {
		return "", &GitError{Msg: "git branch -d " + branch + ": " + firstLine(del.Stderr)}
	}
	sha, err := p.Git.Run("log", "-1", "--format=%h")
	if err != nil {
		return "", err
	}
	return line(sha.Stdout), nil
}

// ChangedFiles lists the files a merge range changes, for the merge-approval
// gate.
func ChangedFiles(g Git, rng string) ([]string, error) {
	res, err := g.Run("diff", "--name-only", rng)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, &GitError{Msg: "git diff " + rng + ": " + firstLine(res.Stderr)}
	}
	return pystr.Splitlines(strings.TrimSpace(res.Stdout)), nil
}

// PRMerger is the issue backend's gated PR merge.
type PRMerger interface {
	MergePRGated(pr int, items []string, approve backlog.MergeApprover) (backlog.MergeResult, error)
}

// PRMergeRefused is a PR the forge refused to merge.
type PRMergeRefused struct{ Err error }

func (e *PRMergeRefused) Error() string { return e.Err.Error() }
func (e *PRMergeRefused) Unwrap() error { return e.Err }

// UnprovenError is a PR held back because linked items have no proof.
type UnprovenError struct{ IDs []string }

func (e *UnprovenError) Error() string { return "an item has no proof; not merged" }

// MergedPR is a merged PR: the short merge hash and the items it closed.
type MergedPR struct {
	SHA    string
	Closed []backlog.ItemRef
}

// MergePR merges PR pr through the gated issue backend. A refused merge is a
// *PRMergeRefused and a proof failure an *UnprovenError; any other error,
// including the approver's own, passes through unchanged.
func MergePR(be PRMerger, pr int, items []string, approve backlog.MergeApprover) (MergedPR, error) {
	res, err := be.MergePRGated(pr, items, approve)
	var mf *backlog.MergeFailedError
	switch {
	case errors.As(err, &mf):
		return MergedPR{}, &PRMergeRefused{Err: err}
	case err != nil:
		return MergedPR{}, err
	case len(res.Unproven) > 0:
		ids := []string{}
		for _, u := range res.Unproven {
			ids = append(ids, u.ID)
		}
		return MergedPR{}, &UnprovenError{IDs: ids}
	}
	return MergedPR{SHA: res.SHA[:min(7, len(res.SHA))], Closed: res.Closed}, nil
}
