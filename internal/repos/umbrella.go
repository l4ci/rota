package repos

import (
	"context"
	"errors"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/rotatree"
)

// Umbrella is hv-umbrella-on: true iff <base>/.rota/repos.json registers at
// least one sub-repo. umbrella.enabled plays no part.
func Umbrella(base string) bool { return len(Load(base)) > 0 }

// Errors of Which, from hv-resolve-repo. A missing .rota/ and a masked
// umbrella are rotatree.ErrNoRoot and rotatree.ErrMasked.
var (
	// ErrGitMissing: no git binary on PATH.
	ErrGitMissing = errors.New("git is not installed")
	// ErrNotGit: the working directory is not inside a git repo.
	ErrNotGit = errors.New("not inside a git repo")
	// ErrNotRegistered: the checkout is not a registered sub-repo.
	ErrNotRegistered = errors.New("not inside a registered sub-repo")
)

// FindRoot is rotatree.FindRoot over root's repos.json, nameless entries
// included: the one umbrella walk-up.
func FindRoot(start string) (string, error) {
	return rotatree.FindRoot(start, subs)
}

// subs is root's repos.json entries for masking. Unlike Load it keeps an entry
// without a name: its path still registers a tree.
func subs(root string) []rotatree.Sub {
	reg, _ := fsio.LoadJSON(rotatree.Repos(root), nil).(*jsonx.Object)
	if reg == nil {
		return nil
	}
	list, _ := reg.Get("repos")
	entries, _ := list.([]any)
	var out []rotatree.Sub
	for _, e := range entries {
		o, ok := e.(*jsonx.Object)
		if !ok {
			continue
		}
		if p := str(o, "path"); p != "" {
			out = append(out, rotatree.Sub{Name: str(o, "name"), Path: p})
		}
	}
	return out
}

// Which is hv-resolve-repo for the directory cwd: the registered sub-repo
// whose checkout cwd belongs to. A Layout B worktree maps to its main repo
// through git's common dir. The registry is the one of the umbrella that
// FindRoot resolves from cwd.
func Which(cwd string) (Repo, error) {
	sub, ok, err := rotastate.MainCheckoutVia(context.Background(), git.Exec, cwd)
	if errors.Is(err, git.ErrNoGit) {
		return Repo{}, ErrGitMissing
	}
	if err != nil || !ok {
		return Repo{}, ErrNotGit
	}
	sub = Realpath(sub)
	umb, err := FindRoot(cwd)
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
