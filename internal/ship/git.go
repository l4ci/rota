package ship

import (
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/strutil"
)

// Git is the one git call the ship rules need. A non-zero exit is a Result;
// err is a failure to run git at all.
type Git interface {
	Run(args ...string) (git.Result, error)
}

// GitError is a git call that exited non-zero where the rules needed it to
// succeed.
type GitError struct{ Msg string }

func (e *GitError) Error() string { return e.Msg }

// out runs git and returns stdout without trailing newlines; a non-zero exit
// is a *GitError.
func out(g Git, args ...string) (string, error) {
	return outAs(g, strings.Join(args, " "), args...)
}

// outAs is out with the command text the error names, for calls whose
// message shows the subject rather than every flag.
func outAs(g Git, label string, args ...string) (string, error) {
	res, err := g.Run(args...)
	if err == nil && res.ExitCode != 0 {
		err = &GitError{Msg: fmt.Sprintf("git %s: %s", label, strutil.FirstLine(res.Stderr))}
	}
	return line(res.Stdout), err
}

func line(s string) string { return strings.TrimRight(s, "\n") }
