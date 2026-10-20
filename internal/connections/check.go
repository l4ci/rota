package connections

import (
	"context"
	"fmt"
)

// Tester makes one authenticated read against a tracker, for a connection
// kind. The Jira and Linear implementations live with their adapters; this is
// the seam `rota connect test` and `rota doctor` call through.
type Tester interface {
	Test(ctx context.Context, c Connection, secret string) error
}

// State is the outcome of checking one connection.
type State string

const (
	StateOK          State = "ok"
	StateUnknown     State = "unknown"     // no such connection
	StateNoSecret    State = "nosecret"    // connection known, no secret anywhere
	StateFailed      State = "failed"      // the test read failed, or a store errored
	StateUnsupported State = "unsupported" // no test read for this kind yet
)

// Diagnosis is what Check found.
type Diagnosis struct {
	State  State
	Detail string
	Hint   string
	// Store is where the secret came from: keychain, file or env.
	Store string
}

// Check runs the full chain for connection name: it exists, has a secret, and
// the kind's test read succeeds. testers is keyed by kind.
func Check(ctx context.Context, dir, name string, sec *Secrets, testers map[string]Tester) Diagnosis {
	c, ok, err := Get(dir, name)
	if err != nil {
		return Diagnosis{State: StateFailed, Detail: err.Error(), Hint: "fix or remove " + Path(dir)}
	}
	if !ok {
		return Diagnosis{State: StateUnknown, Detail: fmt.Sprintf("connection %q is not defined on this machine", name),
			Hint: fmt.Sprintf("run: rota connect add %s --kind jira|linear|itsaplan --site <url>", name)}
	}
	secret, store, err := sec.Resolve(ctx, name)
	if err != nil {
		return Diagnosis{State: StateFailed, Detail: "reading the secret: " + err.Error(), Hint: "run: rota connect add " + name + " --replace"}
	}
	if secret == "" {
		return Diagnosis{State: StateNoSecret, Detail: fmt.Sprintf("connection %q has no secret", name),
			Hint: fmt.Sprintf("run: rota connect add %s --replace, or set %s", name, EnvVar(name))}
	}
	t := testers[c.Kind]
	if t == nil {
		return Diagnosis{State: StateUnsupported, Store: store, Detail: fmt.Sprintf("connection %q (%s) has a secret in %s; no test read for kind %s in this build", name, c.Kind, store, c.Kind)}
	}
	if err := t.Test(ctx, c, secret); err != nil {
		return Diagnosis{State: StateFailed, Store: store, Detail: fmt.Sprintf("connection %q: test read failed: %v", name, err),
			Hint: "check the site and token: rota connect add " + name + " --replace"}
	}
	return Diagnosis{State: StateOK, Store: store, Detail: fmt.Sprintf("connection %q (%s %s): test read passed, secret in %s", name, c.Kind, c.Site, store)}
}
