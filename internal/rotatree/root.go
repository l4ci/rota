package rotatree

import (
	"errors"
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

// ErrNoRoot and ErrMasked are FindRoot's failures.
var (
	ErrNoRoot = errors.New("no .rota/ directory here or in any parent")
	ErrMasked = errors.New("stray .rota/ inside a registered sub-repo masks the umbrella")
)

// FindRoot walks up from start (physical paths) to the nearest directory
// holding .rota/, as hv-walk-up --detect-masking does: when a higher .rota/
// registers that directory's tree as a sub-repo in its repos.json, the
// nearer .rota/ is a stray one and FindRoot returns ErrMasked. registered
// lists a candidate root's sub-repo paths as written in its repos.json.
func FindRoot(start string, registered func(root string) []string) (string, error) {
	dir, err := filepath.EvalSymlinks(start)
	if err != nil {
		dir = start
	}
	var cands []string
	Ascend(dir, func(d string) bool {
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
		for _, rel := range registered(parent) {
			sub := filepath.Join(parent, rel)
			if real, err := filepath.EvalSymlinks(sub); err == nil {
				sub = real
			}
			if first == sub || strings.HasPrefix(first, sub+string(filepath.Separator)) {
				return "", ErrMasked
			}
		}
	}
	return first, nil
}
