package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/projects"
	"github.com/l4ci/rota/internal/repos"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/status"
	"github.com/l4ci/rota/internal/update"
)

// The `rota update`, `rota config show|set|edit|check` and `rota repo which|resolve|
// umbrella` verbs. Shapes, flags and exits are the verb contract's
// (docs/contributing/contract/version-config-repo.md); the old helpers named in
// each `old:` line are the behaviour to match.

func configCommands() []*Command {
	return []*Command{
		{Name: "update", Summary: "check for a newer rota release", Verb: updateVerb},
		{Name: "config", Summary: "read and write .rota/config.json", UISub: "show", Subs: []*Command{
			{Name: "show", Summary: "effective value and source of config keys (--global: the global config)", Repo: true, Verb: configShow, View: configView},
			{Name: "edit", Summary: "change keys interactively (needs a terminal; --global: the global config)", Repo: true, Verb: configEdit},
			{Name: "set", Summary: "set one key in .rota/config.json (--global: the global config)", Repo: true, Verb: configSet},
			{Name: "check", Summary: "compare .rota/config.json with the schema", Repo: true, Verb: configCheck},
			{Name: "fill", Summary: "write the default of every missing required key", Repo: true, Verb: configFill},
			{Name: "save-global", Summary: "merge .rota/config.json into the machine-wide global config", Repo: true, Verb: configSaveGlobal},
		}},
		{Name: "repo", Summary: "umbrella sub-repo registry", Subs: []*Command{
			{Name: "which", Summary: "the registered sub-repo the working directory is in", Verb: repoWhich},
			{Name: "resolve", Summary: "names to registered sub-repo paths", Verb: repoResolve},
			{Name: "umbrella", Summary: "is this an umbrella project", Verb: repoUmbrella},
			{Name: "add", Summary: "register one sub-repo in the umbrella registry", Verb: repoAdd},
			{Name: "rm", Summary: "unregister one sub-repo from the umbrella registry", Verb: repoRm},
		}},
	}
}

// ---- update ----------------------------------------------------------------------

func updateVerb(fs *flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 0, 0, "update takes no arguments"); err != nil {
			return Result{}, err
		}
		env := c.deps().UpdateEnv()
		if env.SkillsNeedUpdate == nil {
			env.SkillsNeedUpdate = func() bool { return skillsNeedUpdate(c) }
		}
		r := update.Check(env)
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

// globalDir is the rota config dir the global config lives in. --global verbs
// use it instead of a project root, so they work outside any project.
func globalDir(c *Ctx) (string, error) {
	if c.Repo != "" {
		return "", Usage("--repo cannot be combined with the global config")
	}
	dir, err := projects.Dir(c.deps().projectsEnv())
	if err != nil {
		return "", Resolution("%v", err)
	}
	return dir, nil
}

func configShow(fs *flag.FlagSet) RunFunc {
	global := fs.Bool("global", false, "show the global config's own values instead of the project's")
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 0, 1, "config show takes at most one key"); err != nil {
			return Result{}, err
		}
		key := ""
		if len(args) == 1 {
			key = args[0]
		}
		if *global || atHome(c) {
			dir, err := globalDir(c)
			if err != nil {
				return Result{}, err
			}
			entries, err := config.ShowGlobal(dir, key, len(args) == 1)
			if errors.Is(err, config.ErrUnknownKey) {
				return Result{}, Resolution("unknown config key '%s'", key)
			}
			if err != nil {
				return Result{}, &Error{Exit: ExitInternal, Message: err.Error()}
			}
			rows, lines := showRows(entries)
			return Result{Data: jsonObj("scope", "global", "entries", rows), Text: strings.Join(lines, "\n")}, nil
		}
		root, err := backlogScope(c)
		if err != nil {
			return Result{}, err
		}
		entries, err := config.Show(root, key, len(args) == 1)
		if err != nil {
			return Result{}, Resolution("unknown config key '%s'", key)
		}
		rows, lines := showRows(entries)
		annotateTierModels(roundcfgOf(root), entries, rows, lines)
		return Result{Data: jsonObj("entries", rows), Text: strings.Join(lines, "\n")}, nil
	}
}

// showRows is `config show`'s Data rows and text lines for entries.
func showRows(entries []config.Entry) (rows []any, lines []string) {
	rows = make([]any, 0, len(entries))
	lines = make([]string, 0, len(entries))
	for _, e := range entries {
		row := jsonObj("key", e.Key, "value", e.Value, "source", e.Source)
		if e.HasGlobal {
			row.Set("global", e.Global)
		}
		if sk := e.Schema; sk != nil {
			row.Set("default", config.Default(*sk))
			row.Set("type", string(sk.Type))
			row.Set("group", sk.Group)
			row.Set("desc", sk.Desc)
			if len(sk.Choices) > 0 {
				row.Set("choices", strSlice(sk.Choices))
			}
		}
		rows = append(rows, row)
		lines = append(lines, e.Line())
	}
	return rows, lines
}

// roundcfgOf is the merged project config, for resolving tier models.
func roundcfgOf(root string) any { return config.Load(rotatree.Config(root)) }

// annotateTierModels adds the effective model and its source to every
// round.tiers.<kind>.<tier> row: after the profile and the models.worker
// fallback, so an empty raw value no longer hides what a worker launches with.
func annotateTierModels(cfg any, entries []config.Entry, rows []any, lines []string) {
	for i, e := range entries {
		kind, tier, ok := tierKeyParts(e.Key)
		if !ok {
			continue
		}
		model, source := roundcfg.Effective(cfg, kind, tier)
		rows[i].(*jsonx.Object).Set("effective", model)
		rows[i].(*jsonx.Object).Set("effectiveSource", source)
		shown := model
		if shown == "" {
			shown = "-"
		}
		lines[i] += fmt.Sprintf("  effective %s (%s)", shown, source)
	}
}

// tierKeyParts splits round.tiers.<kind>.<tier>.
func tierKeyParts(key string) (kind, tier string, ok bool) {
	rest, found := strings.CutPrefix(key, "round.tiers.")
	if !found {
		return "", "", false
	}
	kind, tier, found = strings.Cut(rest, ".")
	return kind, tier, found && roundcfg.ValidTier(tier)
}

// configSaveGlobal merges the project's config.json keys (not config.local.json)
// into the global config, the runtime layer under every project.
func configSaveGlobal(fs *flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 0, 0, "config save-global takes no arguments"); err != nil {
			return Result{}, err
		}
		root, err := backlogScope(c)
		if err != nil {
			return Result{}, err
		}
		dir, err := projects.Dir(c.deps().projectsEnv())
		if err != nil {
			return Result{}, Resolution("%v", err)
		}
		saved, am, err := config.SaveGlobal(root, dir)
		if errors.Is(err, config.ErrCorrupt) || errors.Is(err, fsio.ErrUnreadable) {
			return Result{}, &Error{Exit: ExitInternal, Message: err.Error()}
		}
		if err != nil {
			return Result{}, err
		}
		path := config.GlobalPath(dir)
		data := jsonObj("path", path, "saved", strSlice(saved))
		text := fmt.Sprintf("saved %d keys to %s", len(saved), path)
		if lines := autonomyLines(am); len(lines) > 0 {
			data.Set("autonomy", jsonObj("rewrote", true))
			text += "\n" + strings.Join(lines, "\n")
		}
		return Result{Data: data, Text: text}, nil
	}
}

func configSet(fs *flag.FlagSet) RunFunc {
	global := fs.Bool("global", false, "write the global config instead of .rota/config.json")
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 2, 2, "config set takes a key and a value"); err != nil {
			return Result{}, err
		}
		var res config.SetResult
		var err error
		scope := "project"
		if *global || atHome(c) {
			scope = "global"
			var dir string
			if dir, err = globalDir(c); err != nil {
				return Result{}, err
			}
			res, err = config.SetGlobal(dir, args[0], args[1])
		} else {
			var root string
			if root, err = backlogScope(c); err != nil {
				return Result{}, err
			}
			res, err = config.Set(root, args[0], args[1])
		}
		switch {
		case err == nil:
		case errors.Is(err, config.ErrMalformedKey), errors.Is(err, config.ErrNotSchemaKey), errors.Is(err, config.ErrBadValue), errors.Is(err, config.ErrProjectOwned):
			return Result{}, Usage("%s", err.Error())
		case errors.Is(err, config.ErrNotObject), errors.Is(err, config.ErrGlobalNotObject), errors.Is(err, fsio.ErrUnreadable):
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
		text := args[0] + " = " + string(b)
		if scope == "global" {
			data.Set("scope", "global")
			text = "global: " + text + " (global config, applies to every project that does not override it)"
		}
		if lines := autonomyLines(res.Migration); len(lines) > 0 {
			data.Set("autonomy", jsonObj("rewrote", true))
			text += "\n" + strings.Join(lines, "\n")
		}
		return Result{Data: data, Text: text}, nil
	}
}

// globalCorruptLine is the one wording `config check` and `config fill` share
// when the global config cannot be read.
func globalCorruptLine(gerr error) string {
	return "GLOBAL CORRUPT: " + gerr.Error() + " (inherited keys read as missing; repair the global config, do not run fill)"
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
		removed := config.Removed(root)
		data.Set("removed", anySlice(removed))
		res := Result{Data: data, Text: token}
		if config.AutonomyPending(root).LocalAutopilot {
			removed = append(removed, "round.autopilot (config.local.json)")
			data.Set("removed", anySlice(removed))
		}
		if len(removed) > 0 {
			res.Text += "\nREMOVED: " + strings.Join(removed, ", ") + " (nothing reads them; run: rota config fill)"
		}
		if old := config.LegacyRoundKeys(config.Load(rotatree.Config(root))); len(old) > 0 {
			data.Set("unknown", anySlice(old))
			var names []string
			for _, k := range old {
				names = append(names, k+" (now "+config.RenamedRoundKey(k)+")")
			}
			res.Text += "\nUNKNOWN: " + strings.Join(names, ", ") + " (nothing reads the old names; run: rota migrate config --apply, or rota config fill)"
		}
		if legacy := config.Legacy(root); len(legacy) > 0 {
			data.Set("legacy", anySlice(legacy))
			res.Text += "\nLEGACY: " + strings.Join(legacy, ", ") + " (replaced by issues.source and forge.provider; run: rota migrate source --apply, or rota config fill)"
		}
		if gerr := config.GlobalError(); gerr != nil && status == config.Stale {
			data.Set("globalCorrupt", gerr.Error())
			res.Text += "\n" + globalCorruptLine(gerr)
		}
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
		removed := config.Removed(root)
		am := config.AutonomyPending(root)
		if am.LocalAutopilot {
			removed = append(removed, "round.autopilot (config.local.json)")
		}
		// Same condition as `config check`: STALE with an unreadable global.
		var corrupt error
		if status, _ := config.Check(root); status == config.Stale {
			corrupt = config.GlobalError()
		}
		filled, sm, rm, err := config.FillDetect(root, c.deps().originProvider(c.Context(), root))
		if err != nil {
			return roundMigrationFail(err)
		}
		const upToDate = "config up to date; nothing to fill"
		text := upToDate
		if len(filled) > 0 {
			text = "filled: " + strings.Join(filled, ", ")
		} else if corrupt != nil {
			text = "nothing filled. " + globalCorruptLine(corrupt)
		}
		add := func(line string) {
			if text == upToDate {
				text = line
			} else {
				text += "\n" + line
			}
		}
		if len(removed) > 0 {
			add("removed: " + strings.Join(removed, ", "))
		}
		rewrote := am.Rewrite || am.LocalRewrite || am.GlobalRewrite
		data := jsonObj("filled", anySlice(filled), "removed", anySlice(removed), "changed", len(filled) > 0 || len(removed) > 0 || sm.Changed || rm.Changed() || rewrote)
		data.Set("autonomy", jsonObj("rewrote", rewrote, "autopilotWasOn", am.AutopilotWasOn))
		for _, line := range autonomyLines(am) {
			add(line)
		}
		if rm.Changed() {
			data.Set("renamed", jsonObj("config", renameList(rm.Config), "local", renameList(rm.Local)))
			var moved []string
			for _, r := range append(append([]config.Rename{}, rm.Config...), rm.Local...) {
				moved = append(moved, r.Old+" -> "+r.New)
			}
			add("renamed: " + strings.Join(moved, ", "))
		}
		if sm.Changed {
			data.Set("migrated", jsonObj("source", sm.Source, "forge", sm.Forge, "removed", anySlice(sm.Removed)))
			line := "migrated: " + strings.Join(sm.Removed, ", ") + " -> issues.source " + sm.Source
			if sm.Forge != "" {
				line += ", forge.provider " + sm.Forge
			}
			add(line)
		} else if sm.Unresolved {
			c.Warn("backlog.backend is issues and issues.provider is auto, but the origin remote names neither github nor gitlab: set issues.source by hand")
		}
		return Result{Data: data, Text: text}, nil
	}
}

// autonomyLines are what `config fill` and `rota init` say about the autonomy
// migration (#1101): an old one-hop auto rewritten to roundtrip, and a
// round.autopilot true that no longer turns anything on by itself.
func autonomyLines(am config.AutonomyMigration) []string {
	var out []string
	if am.Rewrite || am.LocalRewrite || am.GlobalRewrite {
		var files []string
		if am.Rewrite {
			files = append(files, "config.json")
		}
		if am.LocalRewrite {
			files = append(files, "config.local.json")
		}
		if am.GlobalRewrite {
			files = append(files, "the global config")
		}
		set := "rota config set autonomy.level auto"
		if am.GlobalRewrite {
			set += ", with --global for the global config"
		}
		out = append(out, "rewrote: autonomy.level auto -> roundtrip in "+strings.Join(files, " and ")+" (auto used to mean one hop; it now keeps taking the next ready item: "+set+")")
	}
	if am.AutopilotWasOn {
		out = append(out, "round.autopilot was true and is removed: the round autopilot now runs only under autonomy.level auto (rota config set autonomy.level auto)")
	}
	return out
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
