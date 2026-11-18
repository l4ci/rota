package land

import (
	"errors"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/strutil"
)

// GitError is a git command that exited non-zero where landing or its caller
// needed it to succeed.
type GitError struct{ Msg string }

func (e *GitError) Error() string { return e.Msg }

// BranchMovedError reports a landed pin whose branch must be kept because its
// current tip differs. LandLocal returns the merge hash alongside this error.
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

// LocalPorts supplies policy before a local landing. Verify receives the pin;
// Prepare runs only after Verify allows it (for example, to clear a worktree).
// Their errors pass through unchanged. Recover must survive caller cancellation;
// nil uses Git, for callers without a cancellable runner.
type LocalPorts struct {
	Git, Recover Git
	Verify       func(head string) error
	Prepare      func() error
}

// LocalRequest names a local landing. Head is an already verified commit, or
// Branch is resolved once before Verify. An empty Checkout leaves the current
// branch alone, for scratch trees and gates already on base. DeleteBranch keeps
// a branch that moved after the pin. ShortSHA preserves ship's abbreviated hash;
// other callers get the full commit ID.
type LocalRequest struct {
	Head, Branch, Base, Message string
	Checkout                    string
	DeleteBranch, ShortSHA      bool
}

// LandLocal owns pin, policy hooks, checkout, merge and read-back for every local
// landing. A post-merge failure is distinct from a merge refusal: the pin has
// landed, even if the caller cannot read its commit ID. MergeLocal owns abort
// and classification of failures during the merge itself.
func LandLocal(p LocalPorts, o LocalRequest) (string, error) {
	head := o.Head
	if head == "" && o.Branch != "" {
		var err error
		head, err = localOut(p.Git, "rev-parse "+o.Branch, "rev-parse", "--verify", "-q", o.Branch+"^{commit}")
		if err != nil {
			return "", err
		}
	}
	if head == "" {
		return "", ErrUnpinned
	}
	if p.Verify != nil {
		if err := p.Verify(head); err != nil {
			return "", err
		}
	}
	if p.Prepare != nil {
		if err := p.Prepare(); err != nil {
			return "", err
		}
	}
	if o.Checkout != "" {
		if _, err := localOut(p.Git, "checkout "+o.Checkout, "checkout", "-q", o.Checkout); err != nil {
			return "", err
		}
	}
	recover := p.Recover
	if recover == nil {
		recover = p.Git
	}
	if err := MergeLocal(p.Git, head, o.Message, recover); err != nil {
		return "", err
	}
	postMerge := func(sha string, err error) error {
		return &PostMergeError{Pinned: head, Base: o.Base, MergeSHA: sha, Err: err}
	}
	readArgs := []string{"rev-parse", "HEAD"}
	if o.ShortSHA {
		readArgs = []string{"log", "-1", "--format=%h"}
	}
	sha, err := localOut(p.Git, strings.Join(readArgs, " "), readArgs...)
	if err != nil || sha == "" {
		if err == nil {
			err = &GitError{Msg: "git " + strings.Join(readArgs, " ") + ": empty commit ID"}
		}
		base := "HEAD"
		if o.ShortSHA && o.Base != "" {
			base = o.Base
		}
		recoveryArgs := []string{"rev-parse", base}
		if o.ShortSHA {
			recoveryArgs = []string{"rev-parse", "--short", base}
		}
		read, readErr := localOut(recover, strings.Join(recoveryArgs, " "), recoveryArgs...)
		if readErr != nil {
			err = errors.Join(err, readErr)
		} else if read != "" {
			return read, postMerge(read, err)
		} else {
			err = errors.Join(err, &GitError{Msg: "git " + strings.Join(recoveryArgs, " ") + ": empty commit ID"})
		}
		return "", postMerge("", err)
	}
	if o.DeleteBranch {
		current, err := localOut(p.Git, "rev-parse "+o.Branch, "rev-parse", "--verify", "-q", o.Branch+"^{commit}")
		if err != nil {
			return sha, postMerge(sha, err)
		}
		if current != head {
			return sha, &BranchMovedError{Branch: o.Branch, Base: o.Base, Pinned: head, Tip: current, MergeSHA: sha}
		}
		if _, err := localOut(p.Git, "branch -d "+o.Branch, "branch", "-d", o.Branch); err != nil {
			return sha, postMerge(sha, err)
		}
	}
	return sha, nil
}

func localOut(run Git, label string, args ...string) (string, error) {
	res, err := run(args...)
	if err == nil && res.ExitCode != 0 {
		err = &GitError{Msg: fmt.Sprintf("git %s: %s", label, strutil.FirstLine(res.Stderr))}
	}
	return strings.TrimRight(res.Stdout, "\n"), err
}
