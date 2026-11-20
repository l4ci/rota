package ship

import (
	"strings"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/land"
)

// Git is the one git call the ship rules need. A non-zero exit is a Result;
// err is a failure to run git at all.
type Git interface {
	Run(args ...string) (git.Result, error)
}

// GitError is a git call that exited non-zero where the rules needed it to
// succeed.
type GitError = land.GitError

// out runs git and returns stdout without trailing newlines; a non-zero exit
// is a *GitError.
func out(g Git, args ...string) (string, error) {
	return outAs(g, strings.Join(args, " "), args...)
}

// outAs is out with the command text the error names, for calls whose
// message shows the subject rather than every flag.
func outAs(g Git, label string, args ...string) (string, error) {
	return land.GitOut(g.Run, label, args...)
}

func line(s string) string { return strings.TrimRight(s, "\n") }
