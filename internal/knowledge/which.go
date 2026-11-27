package knowledge

import (
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/repos"
)

// DefaultScope is the scope a verb without --repo acts on: the sub-repo that
// cwd belongs to, else the umbrella. Membership goes through git's common dir
// as the earlier hv-resolve-repo helper did, so a linked worktree of a sub-repo resolves to it.
// It never fails: anything unresolvable is the umbrella.
func DefaultScope(run git.Runner, cwd string, list []repos.Repo) string {
	if len(list) > 0 {
		if r, err := repos.Lookup(run, cwd, list); err == nil {
			return r.Name
		}
	}
	return Umbrella
}
