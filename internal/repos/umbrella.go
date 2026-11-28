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

// Errors of Lookup and Which, from hv-resolve-repo. A missing .rota/ and a masked
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
		if p := jsonx.Str(o, "path"); p != "" {
			out = append(out, rotatree.Sub{Name: jsonx.Str(o, "name"), Path: p})
		}
	}
	return out
}

// Which is hv-resolve-repo for the directory cwd: the registered sub-repo
// whose checkout cwd belongs to. A Layout B worktree maps to its main repo
// through git's common dir. The registry is the one of the umbrella that
// FindRoot resolves from cwd.
func Which(run git.Runner, cwd string) (Repo, error) {
	umb, rootErr := FindRoot(cwd)
	var list []Repo
	if rootErr == nil {
		list = Load(umb)
	}
	r, err := Lookup(run, cwd, list)
	// Preserve the git errors before root-resolution errors, and root errors
	// before the unregistered fallback.
	if errors.Is(err, ErrGitMissing) || errors.Is(err, ErrNotGit) {
		return Repo{}, err
	}
	if rootErr != nil {
		return Repo{}, rootErr
	}
	return r, err
}

// Lookup finds the first registered sub-repo whose main checkout holds cwd.
// list is in registry order, with resolved paths as returned by Load. Linked
// worktrees and symlinks resolve through the same git common directory.
// A nil runner uses git.Exec.
func Lookup(run git.Runner, cwd string, list []Repo) (Repo, error) {
	if run == nil {
		run = git.Exec
	}
	main, ok, err := rotastate.MainCheckoutVia(context.Background(), run, cwd)
	if errors.Is(err, git.ErrNoGit) {
		return Repo{}, ErrGitMissing
	}
	if err != nil || !ok {
		return Repo{}, ErrNotGit
	}
	main = Realpath(main)
	for _, r := range list {
		if r.Path == main {
			return r, nil
		}
	}
	return Repo{}, ErrNotRegistered
}

// VerdictRepo is the repo name that keys a branch's verdicts: flag when set,
// else in the umbrella at root the sub-repo whose checkout dir belongs to,
// else "" (a plain project, or a dir outside every registered sub-repo).
func VerdictRepo(run git.Runner, root, dir, flag string) string {
	if flag != "" || !Umbrella(root) {
		return flag
	}
	if r, err := Which(run, dir); err == nil {
		return r.Name
	}
	return ""
}
