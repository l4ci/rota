package cli

import (
	"errors"
	"flag"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/rotatree"
)

func configMigrateCommand() *Command {
	return &Command{Name: "config", Summary: "move the round-only work.* keys to round.* in config.json and config.local.json (preview unless --apply)", Verb: migrateConfig}
}

// migrateConfig is `rota migrate config` (#1097): every round-only key under
// work.* moves to its round.* name in both config layers, value unchanged.
// An old and a new name with different values refuse (exit 4) and nothing is
// written. It is idempotent. `rota config fill` and `rota update` run the
// same migration.
func migrateConfig(fs *flag.FlagSet) RunFunc {
	apply := fs.Bool("apply", false, "write the changes; without it only report them")
	return func(c *Ctx, args []string) (Result, error) {
		if err := knNoArgs(args); err != nil {
			return Result{}, err
		}
		root, err := backlogScope(c)
		if err != nil {
			return Result{}, err
		}
		m, err := config.MigrateRoundKeysFiles(root, *apply)
		if err != nil {
			return roundMigrationFail(err)
		}
		data := knObj("changed", m.Changed() && *apply, "planned", m.Changed(), "applied", *apply,
			"config", renameList(m.Config), "local", renameList(m.Local))
		if !m.Changed() {
			return Result{Data: data, Text: "nothing to migrate: no round keys under work.*"}, nil
		}
		var lines []string
		for _, l := range []struct {
			file string
			rs   []config.Rename
		}{{"config.json", m.Config}, {"config.local.json", m.Local}} {
			for _, r := range l.rs {
				lines = append(lines, l.file+"\t"+r.Old+" -> "+r.New)
			}
		}
		if !*apply {
			c.Warn("preview only; pass --apply")
			return Result{Data: data, Text: "would move:\n" + strings.Join(lines, "\n")}, nil
		}
		return Result{Data: data, Text: "moved:\n" + strings.Join(lines, "\n")}, nil
	}
}

func renameList(rs []config.Rename) []any {
	out := []any{}
	for _, r := range rs {
		out = append(out, knObj("from", r.Old, "to", r.New))
	}
	return out
}

// roundMigrationFail maps the round-key migration's errors: disagreeing old
// and new names refuse (exit 4, both values listed), a malformed layer is
// exit 70 naming the file.
func roundMigrationFail(err error) (Result, error) {
	var ce *config.ConflictError
	switch {
	case errors.As(err, &ce):
		list := []any{}
		for _, x := range ce.Conflicts {
			list = append(list, knObj("file", x.File, "from", x.Old, "to", x.New, "fromValue", x.OldValue, "toValue", x.NewValue))
		}
		d := knObj("blockedBy", "config conflict", "conflicts", list, "changed", false)
		return Result{Data: d}, Refused("%s", err.Error())
	case errors.Is(err, config.ErrCorrupt):
		return Result{}, &Error{Exit: ExitInternal, Message: err.Error()}
	}
	return Result{}, err
}

// roundKeyVerbs are the verbs that read the round-only keys. With an old
// work.* name still in the config they refuse (exit 4) rather than run on a
// key nothing reads any more (#1097 D4). Bare rota is the orchestrator
// launcher.
var roundKeyVerbs = []string{"rota round", "rota worker", "rota doctor", "rota orchestrate", "rota keepalive", "rota layout", "rota limit"}

// roundKeyQuiet are the verbs that neither refuse nor warn: the ones that
// migrate or edit the config, and those that must stay silent (hooks, the
// statusline) or never read project state. `worker prompt-check` is the Codex
// prompt hook, which reads no config and must not block a worker's prompt.
// `worker done|inbox|send|reply` run from a worker's own worktree, whose
// tracked config.json can predate the rename: a refusal there would send the
// worker to rewrite tracked config on its branch (#1163). They never read the
// worktree's config. `worker done` does read round.accounts (for the ledger
// headroom), but through the pool root, the main checkout that holds the slot
// registry (returnRoot), so the old names in the worktree never reach it.
var roundKeyQuiet = []string{"rota migrate", "rota config", "rota init", "rota update", "rota setup", "rota hook", "rota statusline", "rota version", "rota skills", "rota worker prompt-check", "rota worker done", "rota worker inbox", "rota worker send", "rota worker reply"}

func pathUnder(path string, groups []string) bool {
	for _, g := range groups {
		if path == g || strings.HasPrefix(path, g+" ") {
			return true
		}
	}
	return false
}

// roundKeysStop refuses a round verb on a config that still names a
// round-only key under work.*, and warns on every other verb. Outside a
// project there is nothing to check.
func roundKeysStop(c *Ctx) (Result, error) {
	if pathUnder(c.Path, roundKeyQuiet) {
		return Result{}, nil
	}
	root, err := c.Root()
	if err != nil {
		return Result{}, nil
	}
	old := config.LegacyRoundKeys(config.Load(rotatree.Config(root)))
	if len(old) == 0 {
		return Result{}, nil
	}
	msg := "config still uses the old round key names " + strings.Join(old, ", ") + ", which nothing reads since they moved to round.*"
	if c.Path == "rota" || pathUnder(c.Path, roundKeyVerbs) {
		d := jsonx.NewObject()
		d.Set("blockedBy", "config migration")
		d.Set("keys", anySlice(old))
		d.Set("changed", false)
		return Result{Data: d}, Refused("%s", msg).WithHint("run: rota migrate config --apply")
	}
	c.Warn("%s; run: rota migrate config --apply", msg)
	return Result{}, nil
}
