package rotatree

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AtUmbrellaRoot reports whether dir is an umbrella root with no git of its
// own: no .git directory, a .rota directory, and at least one registered
// sub-repo (hv-require-git-context). registered reports the last part.
func AtUmbrellaRoot(dir string, registered func() bool) bool {
	if fi, err := os.Stat(filepath.Join(dir, ".git")); err == nil && fi.IsDir() {
		return false
	}
	if !Exists(dir) {
		return false
	}
	return registered()
}

// ErrNoRoot and ErrMasked are FindRoot's failures. A masked walk returns a
// *MaskedError, which errors.Is matches to ErrMasked.
var (
	ErrNoRoot = errors.New("no .rota/ directory here or in any parent")
	ErrMasked = errors.New("stray .rota/ inside a registered sub-repo masks the umbrella")
)

// MaskedError is hv-walk-up --detect-masking's exit 2: a stray .rota/ in a
// registered sub-repo hides the umbrella above it.
type MaskedError struct {
	Name  string // the registered sub-repo that holds the stray .rota/ (may be empty)
	Stray string // the stray .rota/ directory
}

func (e *MaskedError) Error() string {
	return fmt.Sprintf("stray .rota/ inside registered sub-repo %s masks the umbrella", e.Name)
}

// Is makes a *MaskedError match ErrMasked.
func (e *MaskedError) Is(target error) bool { return target == ErrMasked }

// Sub is one repos.json entry as masking sees it: Path as written (relative to
// the registering root, or absolute). An entry without a name still registers
// its tree.
type Sub struct{ Name, Path string }

// FindRoot walks up from start to the nearest directory holding .rota/ (the
// filesystem root itself is never checked), as hv-walk-up --detect-masking
// does: when a higher .rota/ registers that directory's tree as a sub-repo,
// the nearer .rota/ is a stray one and FindRoot returns a *MaskedError.
// registered lists a candidate root's sub-repos as written in its repos.json.
// It is the one umbrella walk-up: repos.Which and the cli root lookup call it.
func FindRoot(start string, registered func(root string) []Sub) (string, error) {
	dir, err := filepath.EvalSymlinks(start)
	if err != nil {
		dir = start
	}
	var cands []string
	Ascend(dir, func(d string) bool {
		if d == string(filepath.Separator) {
			return true
		}
		if Exists(d) {
			cands = append(cands, d)
		}
		return false
	})
	if len(cands) == 0 {
		return "", ErrNoRoot
	}
	first := cands[0]
	for _, parent := range cands[1:] {
		for _, s := range registered(parent) {
			sub := s.Path
			if !filepath.IsAbs(sub) {
				sub = filepath.Join(parent, sub)
			}
			if real, err := filepath.EvalSymlinks(sub); err == nil {
				sub = real
			}
			if first == sub || strings.HasPrefix(first, sub+string(filepath.Separator)) {
				return "", &MaskedError{Name: s.Name, Stray: Dir(first)}
			}
		}
	}
	return first, nil
}
