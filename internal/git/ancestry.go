package git

import (
	"context"
	"fmt"
)

// AncestryError is git failing to answer an ancestry query: a bad ref, no
// repo, any exit other than 0 (ancestor) and 1 (not an ancestor).
type AncestryError struct {
	Ancestor, Descendant string
	ExitCode             int
}

func (e *AncestryError) Error() string {
	return fmt.Sprintf("git merge-base --is-ancestor %s %s exited %d", e.Ancestor, e.Descendant, e.ExitCode)
}

// IsAncestorWith asks whether ancestor is reachable from descendant through
// run, which runs git with the given args and reports its exit code (err for a
// call that never got an answer). It is the one place the exit-code contract
// lives: 0 is yes, 1 is no, anything else is an *AncestryError, so an
// unreadable ref never reads as "not an ancestor". Each caller says what an
// error means for it, a refusal or a skip.
func IsAncestorWith(run func(args ...string) (int, error), ancestor, descendant string) (bool, error) {
	code, err := run("merge-base", "--is-ancestor", ancestor, descendant)
	if err != nil {
		return false, err
	}
	switch code {
	case 0:
		return true, nil
	case 1:
		return false, nil
	}
	return false, &AncestryError{Ancestor: ancestor, Descendant: descendant, ExitCode: code}
}

// IsAncestor is IsAncestorWith over the repo's runner.
func (r Repo) IsAncestor(ctx context.Context, ancestor, descendant string) (bool, error) {
	return IsAncestorWith(func(args ...string) (int, error) {
		res, err := r.Run(ctx, args...)
		return res.ExitCode, err
	}, ancestor, descendant)
}
