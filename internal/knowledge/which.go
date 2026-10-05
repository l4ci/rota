package knowledge

import (
	"context"
	"path/filepath"

	"github.com/l4ci/rota/internal/git"
)

// DefaultScope is the scope a verb without --repo acts on: the sub-repo that
// cwd belongs to, else the umbrella. Membership goes through git's common dir
// as hv-resolve-repo did, so a linked worktree of a sub-repo resolves to it.
// It never fails: anything unresolvable is the umbrella.
func (s Store) DefaultScope(cwd string) string {
	if len(s.Repos) == 0 {
		return Umbrella
	}
	common, ok, err := git.Repo{Dir: cwd}.CommonDir(context.Background())
	if err != nil || !ok {
		return Umbrella
	}
	top := filepath.Dir(common)
	if real, err := filepath.EvalSymlinks(top); err == nil {
		top = real
	}
	for name, p := range s.Repos {
		if p == top {
			return name
		}
	}
	return Umbrella
}
