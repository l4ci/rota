package round

import (
	"context"
	"strings"

	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/roundcfg"
)

// PostPointer leaves one comment on issue id that points at a post made on its
// PR (#1827): the issue thread is the index the other machine's watch reads.
// kind names the post (bounce, pick, review), url is its link, and the comment
// carries `rota:pr-pointer <kind>` and the posting machine's role. It posts
// only with the support split active (round.support set); without it, an
// ordinary round, it posts nothing and returns "". Callers treat a failure as
// a warning: the post on the PR already went out.
func (e Env) PostPointer(ctx context.Context, be Board, id, kind, url string) (string, error) {
	if e.Round.Support == "" {
		return "", nil
	}
	role := e.Round.Role
	if role != roundcfg.RoleSupport {
		role = roundcfg.RoleMain
	}
	body := marker.PointerLine(kind) + "\n" + marker.FromLine(role) + "\n" + strings.TrimSpace(url)
	return be.AddComment(id, "feedback", body)
}
