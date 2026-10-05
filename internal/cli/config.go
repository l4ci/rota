package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/repos"
	"github.com/l4ci/rota/internal/status"
	"github.com/l4ci/rota/internal/update"
)

// The `rota update`, `rota config show|set|check` and `rota repo which|resolve|
// umbrella` verbs. Shapes, flags and exits are the verb contract's
// (docs/design/contract/version-config-repo.md); the old helpers named in
// each `old:` line are the behaviour to match.

func configCommands() []*Command {
	return []*Command{
		{Name: "update", Summary: "check for a newer rota release", Verb: updateVerb},
		{Name: "config", Summary: "read and write .rota/config.json", Subs: []*Command{
			{Name: "show", Summary: "effective value and source of config keys", Repo: true, Verb: configShow},
			{Name: "set", Summary: "set one key in .rota/config.json", Repo: true, Verb: configSet},
			{Name: "check", Summary: "compare .rota/config.json with the schema", Repo: true, Verb: configCheck},
			{Name: "fill", Summary: "write the default of every missing required key", Repo: true, Verb: configFill},
		}},
		{Name: "repo", Summary: "umbrella sub-repo registry", Subs: []*Command{
			{Name: "which", Summary: "the registered sub-repo the working directory is in", Verb: repoWhich},
			{Name: "resolve", Summary: "names to registered sub-repo paths", Verb: repoResolve},
			{Name: "umbrella", Summary: "is this an umbrella project", Verb: repoUmbrella},
		}},
	}
}

// ---- update ----------------------------------------------------------------------

func updateVerb(fs *flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 0, 0, "update takes no arguments"); err != nil {
			return Result{}, err
		}
		r := update.Check(c.deps().UpdateEnv())
		data := jsonObj("installType", r.InstallType, "installRoot", r.InstallRoot,
			"currentVersion", r.CurrentVersion, "latestVersion", r.LatestVersion,
			"status", r.Status, "updateCommand", r.UpdateCommand)
		text := fmt.Sprintf("rota %s (%s), latest %s: %s", orDash(r.CurrentVersion), r.InstallType, orDash(r.LatestVersion), r.Status)
		if r.Status == "behind" {
			text += "\nupdate with: " + r.UpdateCommand
		}
		return Result{Data: data, Text: text}, nil
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// ---- config ----------------------------------------------------------------------

func configShow(fs *flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 0, 1, "config show takes at most one key"); err != nil {
			return Result{}, err
		}
		root, err := backlogScope(c)
		if err != nil {
			return Result{}, err
		}
		key := ""
		if len(args) == 1 {
			key = args[0]
		}
		entries, err := config.Show(root, key, len(args) == 1)
		if err != nil {
			return Result{}, Resolution("unknown config key '%s'", key)
		}
		rows := make([]any, 0, len(entries))
		lines := make([]string, 0, len(entries))
		for _, e := range entries {
			rows = append(rows, jsonObj("key", e.Key, "value", e.Value, "source", e.Source))
			lines = append(lines, e.Line())
		}
		return Result{Data: jsonObj("entries", rows), Text: strings.Join(lines, "\n")}, nil
	}
}

func configSet(fs *flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 2, 2, "config set takes a key and a value"); err != nil {
			return Result{}, err
		}
		root, err := backlogScope(c)
		if err != nil {
			return Result{}, err
		}
		res, err := config.Set(root, args[0], args[1])
		switch {
		case err == nil:
		case errors.Is(err, config.ErrMalformedKey), errors.Is(err, config.ErrNotSchemaKey):
			return Result{}, Usage("%s", err.Error())
		case errors.Is(err, config.ErrNotObject):
			return Result{}, &Error{Exit: ExitInternal, Message: err.Error()}
		default:
			return Result{}, err
		}
		data := jsonObj("key", args[0], "value", res.Value)
		if res.HadPrevious {
			data.Set("previous", res.Previous)
		}
		data.Set("changed", res.Changed)
		b, _ := jsonx.MarshalCompact(res.Value)
		return Result{Data: data, Text: args[0] + " = " + string(b)}, nil
	}
}

func configCheck(fs *flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 0, 0, "config check takes no arguments"); err != nil {
			return Result{}, err
		}
		root, err := backlogScope(c)
		if err != nil {
			return Result{}, err
		}
		status, missing := config.Check(root)
		data := jsonObj("status", status, "upToDate", status == config.UpToDate, "missing", anySlice(missing))
		token := map[string]string{config.UpToDate: "UP_TO_DATE", config.Fresh: "FRESH", config.Corrupt: "CORRUPT"}[status]
		if status == config.Stale {
			token = "STALE:" + strings.Join(missing, ",")
		}
		res := Result{Data: data, Text: token}
		if status == config.UpToDate || status == config.Stale {
			if retired := config.Retired(root); len(retired) > 0 {
				data.Set("retired", anySlice(retired))
				res.Text = "RETIRED: " + strings.Join(retired, "; ")
				return res, Failed("%s", retired[0])
			}
		}
		switch status {
		case config.UpToDate:
			return res, nil
		case config.Stale:
			return res, Failed("config is stale: %d required keys missing", len(missing))
		case config.Fresh:
			return res, Failed("no .rota/config.json yet")
		}
		return res, Failed(".rota/config.json is not a valid JSON object")
	}
}

// configFill is the A9 `rota config fill` (contract G1): `config check`'s
// missing keys get their schema defaults.
func configFill(fs *flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 0, 0, "config fill takes no arguments"); err != nil {
			return Result{}, err
		}
		root, err := backlogScope(c)
		if err != nil {
			return Result{}, err
		}
		filled, err := config.Fill(root)
		if errors.Is(err, config.ErrCorrupt) {
			return Result{}, &Error{Exit: ExitInternal, Message: err.Error()}
		}
		if err != nil {
			return Result{}, err
		}
		text := "config up to date; nothing to fill"
		if len(filled) > 0 {
			text = "filled: " + strings.Join(filled, ", ")
		}
		return Result{Data: jsonObj("filled", anySlice(filled), "changed", len(filled) > 0), Text: text}, nil
	}
}

// ---- repo ------------------------------------------------------------------------

func repoWhich(fs *flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 0, 0, "repo which takes no arguments"); err != nil {
			return Result{}, err
		}
		cwd, err := os.Getwd()
		if err != nil {
			return Result{}, err
		}
		r, err := repos.Which(cwd)
		var masked *repos.MaskedError
		switch {
		case err == nil:
			return Result{Data: jsonObj("name", r.Name, "path", r.Path), Text: r.Name}, nil
		case errors.Is(err, repos.ErrGitMissing):
			return Result{}, Unavailable("git is not installed")
		case errors.As(err, &masked):
			return Result{}, Resolution("%s", masked.Error()).WithHint("remove " + masked.Stray + " or run from the umbrella root")
		case errors.Is(err, repos.ErrNotGit):
			return Result{}, Resolution("not inside a git repo")
		case errors.Is(err, repos.ErrNoUmbrella):
			return Result{}, Resolution("no umbrella: no .rota/ here or in any parent")
		}
		return Result{}, Resolution("not inside a registered sub-repo")
	}
}

func repoResolve(fs *flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		names := status.ParseReposCSV(strings.Join(args, ","))
		if len(names) == 0 {
			return Result{Data: jsonObj("repos", []any{})}, nil
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		registry := repos.Load(root)
		if missing := status.Missing(registry, names); len(missing) > 0 {
			e := Resolution("unregistered sub-repo(s): %s", strings.Join(missing, ", "))
			if len(registry) == 0 {
				e.WithHint("not an umbrella project: .rota/repos.json registers no sub-repos")
			}
			return Result{}, e
		}
		path := map[string]string{}
		for _, r := range registry {
			path[r.Name] = r.Path
		}
		rows := make([]any, 0, len(names))
		lines := make([]string, 0, len(names))
		for _, n := range names {
			rows = append(rows, jsonObj("name", n, "path", path[n]))
			lines = append(lines, n+" "+path[n])
		}
		return Result{Data: jsonObj("repos", rows), Text: strings.Join(lines, "\n")}, nil
	}
}

func repoUmbrella(fs *flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 0, 0, "repo umbrella takes no arguments"); err != nil {
			return Result{}, err
		}
		on := false
		if root, err := c.Root(); err == nil {
			on = repos.Umbrella(root)
		}
		if on {
			return Result{Data: jsonObj("umbrella", true), Text: "yes"}, nil
		}
		return Result{Data: jsonObj("umbrella", false), Text: "no"}, Failed("not an umbrella project")
	}
}
