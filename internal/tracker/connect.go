package tracker

import (
	"context"
	"fmt"
	"time"

	"github.com/l4ci/rota/internal/cfgdir"
	"github.com/l4ci/rota/internal/connections"
)

// restDeps is the connection and clock state needed by REST trackers.
// The factory copies these from options once, leaving forge process state behind.
type restDeps struct {
	Conn     ConnResolver
	Sleep    func(time.Duration)
	Env      cfgdir.Env
	Keychain func() connections.Store
}

func (c *CLI) restDeps() restDeps {
	return restDeps{Conn: c.Conn, Sleep: c.Sleep, Env: c.Env, Keychain: c.Keychain}
}

// resolver is the connection lookup of d: the injected one, else the machine's.
func (d restDeps) resolver() ConnResolver {
	if d.Conn != nil {
		return d.Conn
	}
	return d.machineConn
}

// machineConn is the connection lookup of `rota connect`: connections.json
// under the global config dir, the secret from ROTA_<NAME>_TOKEN, the
// keychain or the file store.
func (d restDeps) machineConn(ctx context.Context, name string) (connections.Connection, string, error) {
	if d.Env.Getenv == nil || d.Env.Home == nil {
		return connections.Connection{}, "", fmt.Errorf("no machine environment to find connection %q in", name)
	}
	dir, sec, err := connections.Machine(d.Env, d.Keychain)
	if err != nil {
		return connections.Connection{}, "", err
	}
	conn, ok, err := connections.Get(dir, name)
	if err != nil {
		return connections.Connection{}, "", err
	}
	if !ok {
		return connections.Connection{}, "", fmt.Errorf("connection %q is not defined on this machine (run: rota connect add %s --kind jira|linear|itsaplan ...)", name, name)
	}
	secret, _, err := sec.Resolve(ctx, name)
	if err != nil {
		return connections.Connection{}, "", fmt.Errorf("reading the secret of connection %q: %v", name, err)
	}
	return conn, secret, nil
}

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
// a step that points an adapter's REST client at d.Sleep. missing is the
// adapter's own refusal for an unset setting (empty when none); it is raised
// after the connection check and before the lookup, so the first thing the
// user must set is the first thing named.
func (d restDeps) restConn(ctx context.Context, s Settings, spec restSpec, missing string) (connections.Connection, string, func(*REST), error) {
	if s.Connection == "" {
		return connections.Connection{}, "", nil, Unavailable("issues.source is %s but issues.connection is not set (run: rota connect add <name> %s, then rota config set issues.connection <name>)", spec.kind, spec.add)
	}
	if missing != "" {
		return connections.Connection{}, "", nil, Unavailable("%s", missing)
	}
	conn, secret, err := d.resolver()(ctx, s.Connection)
	if err != nil {
		return connections.Connection{}, "", nil, Unavailable("%v", err)
	}
	if err := validateRESTConnection(conn, secret, spec); err != nil {
		return connections.Connection{}, "", nil, err
	}
	return conn, secret, func(r *REST) {
		if sleep := d.Sleep; sleep != nil {
			r.Sleep = func(ctx context.Context, d time.Duration) error {
				sleep(d)
				return ctx.Err()
			}
		}
	}, nil
}

// validateRESTConnection is the refusal shared by resolved and direct construction.
func validateRESTConnection(conn connections.Connection, secret string, spec restSpec) error {
	if conn.Kind != spec.kind {
		return Unavailable("connection %q is kind %s, not %s", conn.Name, conn.Kind, spec.kind)
	}
	if err := conn.Validate(); err != nil {
		return Unavailable("%v (run: rota connect add %s --replace ...)", err, conn.Name)
	}
	if secret == "" {
		return Unavailable("connection %q has no secret (run: rota connect add %s --replace, or set %s)", conn.Name, conn.Name, connections.EnvVar(conn.Name))
	}
	return nil
}
