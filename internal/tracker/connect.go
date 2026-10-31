package tracker

import (
	"context"
	"time"

	"github.com/l4ci/rota/internal/connections"
)

// restSpec names what a REST adapter needs of its connection: the kind it
// speaks to and the `rota connect add` flags that define one.
type restSpec struct {
	kind string
	add  string
}

var (
	jiraSpec     = restSpec{connections.KindJira, "--kind jira ..."}
	linearSpec   = restSpec{connections.KindLinear, "--kind linear"}
	itsaplanSpec = restSpec{connections.KindItsaplan, "--kind itsaplan --site <url>"}
)

// restConn resolves issues.connection for spec: the connection, its secret and
// a step that points an adapter's REST client at c.Sleep. missing is the
// adapter's own refusal for an unset setting (empty when none); it is raised
// after the connection check and before the lookup, so the first thing the
// user must set is the first thing named.
func (c *CLI) restConn(ctx context.Context, s Settings, spec restSpec, missing string) (connections.Connection, string, func(*REST), error) {
	if s.Connection == "" {
		return connections.Connection{}, "", nil, Unavailable("issues.source is %s but issues.connection is not set (run: rota connect add <name> %s, then rota config set issues.connection <name>)", spec.kind, spec.add)
	}
	if missing != "" {
		return connections.Connection{}, "", nil, Unavailable("%s", missing)
	}
	conn, secret, err := c.resolver()(ctx, s.Connection)
	if err != nil {
		return connections.Connection{}, "", nil, Unavailable("%v", err)
	}
	if conn.Kind != spec.kind {
		return connections.Connection{}, "", nil, Unavailable("connection %q is kind %s, not %s", conn.Name, conn.Kind, spec.kind)
	}
	if err := conn.Validate(); err != nil {
		return connections.Connection{}, "", nil, Unavailable("%v (run: rota connect add %s --replace ...)", err, conn.Name)
	}
	if secret == "" {
		return connections.Connection{}, "", nil, Unavailable("connection %q has no secret (run: rota connect add %s --replace, or set %s)", conn.Name, conn.Name, connections.EnvVar(conn.Name))
	}
	return conn, secret, func(r *REST) {
		if sleep := c.Sleep; sleep != nil {
			r.Sleep = func(ctx context.Context, d time.Duration) error {
				sleep(d)
				return ctx.Err()
			}
		}
	}, nil
}
