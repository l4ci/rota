package repos

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
)

// Umbrella is hv-umbrella-on: true iff <base>/.rota/repos.json registers at
// least one sub-repo. umbrella.enabled plays no part.
func Umbrella(base string) bool { return len(Load(base)) > 0 }

// Errors of Which, from hv-resolve-repo, hv-resolve-umbrella and hv-walk-up.
var (
	// ErrGitMissing: no git binary on PATH.
	ErrGitMissing = errors.New("git is not installed")
	// ErrNotGit: the working directory is not inside a git repo.
	ErrNotGit = errors.New("not inside a git repo")
	// ErrNoUmbrella: no .rota/ directory here or above.
	ErrNoUmbrella = errors.New("no .rota/ found here or in any parent")
	// ErrNotRegistered: the checkout is not a registered sub-repo.
	ErrNotRegistered = errors.New("not inside a registered sub-repo")
)

// MaskedError is hv-walk-up --detect-masking's exit 2: a stray .rota/ in a
// registered sub-repo hides the umbrella above it.
type MaskedError struct {
	Name  string // the registered sub-repo that holds the stray .rota/
	Stray string // the stray .rota/ directory
}

func (e *MaskedError) Error() string {
	return fmt.Sprintf("stray .rota/ inside registered sub-repo %s masks the umbrella", e.Name)
}

// FindUmbrella is hv-resolve-umbrella from start: it walks up for .rota/
// directories (the filesystem root itself is never checked, as in the old
// helper), returns the nearest one's parent, and fails with a *MaskedError
// when a higher .rota/ registers that parent's tree as a sub-repo.
func FindUmbrella(start string) (string, error) {
	var cands []string
	dir := Realpath(start)
	for dir != "/" && dir != filepath.Dir(dir) {
		if fi, err := os.Stat(filepath.Join(dir, ".rota")); err == nil && fi.IsDir() {
			cands = append(cands, dir)
		}
		dir = filepath.Dir(dir)
	}
	if len(cands) == 0 {
		return "", ErrNoUmbrella
	}
	first := cands[0]
	firstReal := Realpath(first)
	for _, parent := range cands[1:] {
		reg, _ := fsio.LoadJSON(filepath.Join(parent, ".rota", "repos.json"), nil).(*jsonx.Object)
		if reg == nil {
			continue
		}
		list, _ := reg.Get("repos")
		entries, _ := list.([]any)
		for _, e := range entries {
			o, ok := e.(*jsonx.Object)
			if !ok {
				continue
			}
			rel := str(o, "path")
			if rel == "" {
				continue
			}
			if !filepath.IsAbs(rel) {
				rel = filepath.Join(parent, rel)
			}
			if within(firstReal, Realpath(rel)) {
				return "", &MaskedError{Name: str(o, "name"), Stray: filepath.Join(first, ".rota")}
			}
		}
	}
	return first, nil
}

// within is whether path is dir or lies below it.
func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// Which is hv-resolve-repo for the directory cwd: the registered sub-repo
// whose checkout cwd belongs to. A Layout B worktree maps to its main repo
// through git's common dir. The registry is the one of the umbrella that
// FindUmbrella resolves from cwd.
func Which(cwd string) (Repo, error) {
	git, err := exec.LookPath("git")
	if err != nil {
		return Repo{}, ErrGitMissing
	}
	cmd := exec.Command(git, "rev-parse", "--git-common-dir")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return Repo{}, ErrNotGit
	}
	common := strings.TrimRight(string(out), "\n")
	if !filepath.IsAbs(common) {
		common = filepath.Join(cwd, common)
	}
	sub := Realpath(filepath.Dir(common))
	umb, err := FindUmbrella(cwd)
	if err != nil {
		return Repo{}, err
	}
	for _, r := range Load(umb) {
		if r.Path == sub {
			return r, nil
		}
	}
	return Repo{}, ErrNotRegistered
}

func str(o *jsonx.Object, key string) string {
	v, _ := o.Get(key)
	s, _ := v.(string)
	return s
}
