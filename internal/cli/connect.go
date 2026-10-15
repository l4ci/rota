package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/l4ci/rota/internal/connections"
	"github.com/l4ci/rota/internal/doctor"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/projects"
)

// connectCommand is `rota connect` (#799): machine-wide tracker connections
// and their secrets, kept in the global rota config dir. A project only names
// one (issues.connection). None of these verbs needs a project root.
func connectCommand() *Command {
	return &Command{Name: "connect", Summary: "manage machine-wide tracker connections (Jira, Linear) and their secrets", Subs: []*Command{
		{Name: "add", Summary: "add or replace a named connection and store its token", Verb: connectAddVerb},
		{Name: "list", Summary: "list connections and which store holds each secret; never the secret", Verb: noFlags(runConnectList)},
		{Name: "test", Summary: "make one authenticated read through a connection", Verb: noFlags(runConnectTest)},
		{Name: "remove", Summary: "remove a connection and its stored secret", Verb: noFlags(runConnectRemove)},
	}}
}

// connectEnv is the global config dir and the secret stores a connect verb works against.
func (c *Ctx) connectEnv() (string, *connections.Secrets, error) {
	return c.deps().connectEnv()
}

func (d *Deps) connectEnv() (string, *connections.Secrets, error) {
	dir, err := projects.Dir()
	if err != nil {
		return "", nil, &Error{Exit: ExitInternal, Message: err.Error()}
	}
	sec := &connections.Secrets{Getenv: d.Getenv, File: connections.FileStore(dir)}
	if d.Keychain != nil {
		if kc := d.Keychain(); kc != nil {
			sec.Keychain = kc
		}
	}
	return dir, sec, nil
}

// doctorConnection checks the connection a project names, for `rota doctor`.
func doctorConnection(ctx context.Context, d *Deps, name string) *doctor.ConnCheck {
	dir, sec, err := d.connectEnv()
	if err != nil {
		return &doctor.ConnCheck{Name: name, State: string(connections.StateFailed), Detail: err.Error()}
	}
	dg := connections.Check(ctx, dir, name, sec, d.ConnTesters)
	return &doctor.ConnCheck{Name: name, State: string(dg.State), Detail: dg.Detail, Hint: dg.Hint}
}

func connectAddVerb(fs *flag.FlagSet) RunFunc {
	kind := fs.String("kind", "", "connection kind: jira or linear")
	site := fs.String("site", "", "site URL, e.g. https://acme.atlassian.net (required for jira)")
	user := fs.String("user", "", "account name or email the token belongs to")
	stdin := fs.Bool("token-stdin", false, "read the API token from stdin (the default when stdin is not a terminal)")
	replace := fs.Bool("replace", false, "replace an existing connection of this name")
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) != 1 {
			return Result{}, Usage("connect add takes one connection name").WithHint("run: rota connect add <name> --kind jira --site <url> --token-stdin")
		}
		if !*stdin && c.deps().IsTerminal(c.Stdin) {
			return Result{}, Usage("connect add reads the token from stdin").WithHint("pipe it in: echo \"$TOKEN\" | rota connect add <name> ... --token-stdin")
		}
		raw, err := io.ReadAll(c.Stdin)
		if err != nil {
			return Result{}, &Error{Exit: ExitInternal, Message: "reading the token: " + err.Error()}
		}
		secret := strings.TrimSpace(string(raw))
		if secret == "" {
			return Result{}, Usage("connect add needs a token on stdin").WithHint("pipe it in: echo \"$TOKEN\" | rota connect add <name> ... --token-stdin")
		}
		dir, sec, err := c.connectEnv()
		if err != nil {
			return Result{}, err
		}
		conn := connections.Connection{Name: args[0], Kind: *kind, Site: strings.TrimRight(*site, "/"), User: *user}
		if err := conn.Validate(); err != nil {
			return Result{}, Usage("%s", err.Error())
		}
		_, ok, err := connections.Get(dir, conn.Name)
		if err != nil {
			return Result{}, &Error{Exit: ExitInternal, Message: err.Error()}
		}
		if ok && !*replace {
			return Result{}, Refused("%s: %s", connections.ErrExists, conn.Name).WithHint("pass --replace to overwrite it")
		}
		// Secret first, record second: a failed save never leaves a record
		// without its secret. A failed record write restores the old secret.
		oldSecret, _, _ := sec.Stored(c.Context(), conn.Name)
		saved, err := sec.Save(c.Context(), conn.Name, secret)
		if err != nil {
			return Result{}, &Error{Exit: ExitInternal, Message: "storing the secret: " + err.Error()}
		}
		if err := connections.Add(dir, conn, *replace); err != nil {
			if oldSecret != "" {
				sec.Save(c.Context(), conn.Name, oldSecret)
			} else {
				sec.Delete(c.Context(), conn.Name)
			}
			if errors.Is(err, connections.ErrExists) || errors.Is(err, connections.ErrEnvCollision) {
				return Result{}, Refused("%s", err.Error())
			}
			return Result{}, &Error{Exit: ExitInternal, Message: err.Error()}
		}
		text := fmt.Sprintf("added\t%s\t%s\tsecret in %s", conn.Name, conn.Kind, saved.Store)
		if saved.Note != "" {
			text += "\n" + saved.Note
		}
		data := knObj("name", conn.Name, "kind", conn.Kind, "site", conn.Site, "user", conn.User, "store", saved.Store, "note", saved.Note, "changed", true)
		return Result{Data: data, Text: text}, nil
	}
}

func runConnectList(c *Ctx, args []string) (Result, error) {
	if err := knNoArgs(args); err != nil {
		return Result{}, err
	}
	dir, sec, err := c.connectEnv()
	if err != nil {
		return Result{}, err
	}
	cs, err := connections.Load(dir)
	if err != nil {
		return Result{}, &Error{Exit: ExitInternal, Message: err.Error()}
	}
	rows := []any{}
	var lines []string
	for _, cn := range cs {
		_, store, err := sec.Stored(c.Context(), cn.Name)
		errMsg := ""
		if err != nil {
			store, errMsg = "error", err.Error()
		}
		if store == "" {
			store = "none"
		}
		override := sec.Getenv != nil && strings.TrimSpace(sec.Getenv(connections.EnvVar(cn.Name))) != ""
		rows = append(rows, knObj("name", cn.Name, "kind", cn.Kind, "site", cn.Site, "user", cn.User, "store", store, "storeError", errMsg, "envOverride", override))
		line := fmt.Sprintf("%s\t%s\t%s\t%s\t%s", cn.Name, cn.Kind, cn.Site, cn.User, store)
		if errMsg != "" {
			line += ": " + errMsg
		}
		if override {
			line += "\t(" + connections.EnvVar(cn.Name) + " set)"
		}
		lines = append(lines, line)
	}
	if len(cs) == 0 {
		lines = append(lines, "no connections: run rota connect add <name> --kind jira|linear --site <url> --token-stdin")
	}
	data := jsonx.NewObject()
	data.Set("dir", dir)
	data.Set("connections", rows)
	return Result{Data: data, Text: strings.Join(lines, "\n")}, nil
}

func runConnectTest(c *Ctx, args []string) (Result, error) {
	if len(args) != 1 {
		return Result{}, Usage("connect test takes one connection name").WithHint("run: rota connect test <name>")
	}
	dir, sec, err := c.connectEnv()
	if err != nil {
		return Result{}, err
	}
	dg := connections.Check(c.Context(), dir, args[0], sec, c.deps().ConnTesters)
	text := string(dg.State) + "\t" + dg.Detail
	if dg.Hint != "" {
		text += "\n\thint: " + dg.Hint
	}
	res := Result{Data: knObj("name", args[0], "state", string(dg.State), "detail", dg.Detail, "hint", dg.Hint, "store", dg.Store), Text: text}
	switch dg.State {
	case connections.StateOK:
		return res, nil
	case connections.StateUnsupported:
		// The connection and its secret are fine; this build cannot read the tracker yet.
		return res, Unavailable("%s", dg.Detail)
	}
	return res, Failed("%s", dg.Detail)
}

func runConnectRemove(c *Ctx, args []string) (Result, error) {
	if len(args) != 1 {
		return Result{}, Usage("connect remove takes one connection name").WithHint("run: rota connect remove <name>")
	}
	dir, sec, err := c.connectEnv()
	if err != nil {
		return Result{}, err
	}
	gone, err := connections.Remove(dir, args[0])
	if err != nil {
		return Result{}, &Error{Exit: ExitInternal, Message: err.Error()}
	}
	// Clear the secret even for an unknown name: a half-removed connection must not leave one behind.
	if err := sec.Delete(c.Context(), args[0]); err != nil {
		return Result{}, &Error{Exit: ExitInternal, Message: "clearing the secret: " + err.Error()}
	}
	text := "removed\t" + args[0]
	if !gone {
		text = "noop: no connection named " + args[0]
	}
	return Result{Data: knObj("name", args[0], "removed", gone, "changed", gone), Text: text}, nil
}
