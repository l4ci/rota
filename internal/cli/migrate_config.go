package cli

import (
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/projects"
)

func configMigrateCommand() *Command {
	return &Command{Name: "config", Summary: "move the round-only work.* keys to round.*, fold the codex and opencode account keys into round.accounts, merge and rename the keys #1825 folded together (models.*, the *Command keys, round.workerKind, issues.label), and move machine and default taste keys out of config.json (preview unless --apply)", Verb: migrateConfig}
}

// migrateConfig is `rota migrate config` (#1097): every round-only key under
// work.* moves to its round.* name in both config layers, value unchanged.
// An old and a new name with different values refuse (exit 4) and nothing is
// written. It is idempotent. `rota config fill` and `rota init` run the
// same migration. Then the accounts step (#1824, migrateAccounts) folds
// round.codexAccounts and round.opencodeAccounts into one round.accounts list
// in the global, tracked and local files. Then the merges step (#1825,
// migrateMerges) moves the merged and renamed keys (models.orchestrator,
// round.codexCommand, round.workerKind ...) to their new names; an old and a
// new key with different values refuse (exit 4). Then the scope step (#1786, config.MigrateScopeFiles) moves
// machine keys out of config.json and drops taste keys pinned at their
// default; --keep names keys it leaves in place.
func migrateConfig(fs *flag.FlagSet) RunFunc {
	apply := fs.Bool("apply", false, "write the changes; without it only report them")
	var keep setAnswersList
	fs.Var(&keep, "keep", "leave this key in .rota/config.json (repeatable, or comma-separated)")
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
		am, err := migrateAccounts(c, root, *apply)
		if err != nil {
			return roundMigrationFail(err)
		}
		mm, err := migrateMerges(c, root, *apply)
		if err != nil {
			return roundMigrationFail(err)
		}
		sm, err := config.MigrateScopeFiles(root, *apply, keep)
		if err != nil {
			return roundMigrationFail(err)
		}
		planned := m.Changed() || am.Changed() || mm.Changed() || sm.Changed()
		data := knObj("changed", planned && *apply, "planned", planned, "applied", *apply,
			"config", renameList(m.Config), "local", renameList(m.Local), "accounts", accountList(am), "merges", mergeList(mm), "scope", scopeList(sm))
		if !planned {
			return Result{Data: data, Text: "nothing to migrate: no round keys under work.*, no per-harness account keys, no merged or renamed keys, no machine or default taste keys in .rota/config.json"}, nil
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
		for _, ch := range am.Changes {
			lines = append(lines, ch.File+"\t"+strings.Join(ch.From, ", ")+" -> round.accounts ("+fmt.Sprint(ch.N)+" entries)")
		}
		for _, ch := range mm.Changes {
			lines = append(lines, ch.File+"\t"+strings.Join(ch.From, ", ")+" -> merged into the new keys")
		}
		for _, ch := range sm.Changes {
			lines = append(lines, scopeLine(ch))
		}
		if !*apply {
			c.Warn("preview only; pass --apply")
			return Result{Data: data, Text: "would change:\n" + strings.Join(lines, "\n")}, nil
		}
		return Result{Data: data, Text: "changed:\n" + strings.Join(lines, "\n")}, nil
	}
}

// migrateAccounts is the accounts step of `rota migrate config`: its own call
// beside the round-key and scope steps, so a sibling step can be added next to
// it without reshaping this one. The global config it rewrites is the rota
// config dir's.
func migrateAccounts(c *Ctx, root string, apply bool) (config.AccountsMigration, error) {
	gdir, err := projects.Dir(c.deps().projectsEnv())
	if err != nil {
		gdir = ""
	}
	return config.MigrateAccountsFiles(root, gdir, apply)
}

// migrateMerges is the merges step of `rota migrate config` (#1825), a call of
// its own beside the accounts step.
func migrateMerges(c *Ctx, root string, apply bool) (config.MergesMigration, error) {
	gdir, err := projects.Dir(c.deps().projectsEnv())
	if err != nil {
		gdir = ""
	}
	return config.MigrateMergesFiles(root, gdir, apply)
}

func mergeList(mm config.MergesMigration) []any {
	out := []any{}
	for _, ch := range mm.Changes {
		out = append(out, knObj("file", ch.File, "from", anySlice(ch.From)))
	}
	return out
}

func accountList(am config.AccountsMigration) []any {
	out := []any{}
	for _, ch := range am.Changes {
		out = append(out, knObj("file", ch.File, "from", anySlice(ch.From), "entries", ch.N))
	}
	return out
}

// setAnswersList is a repeatable flag whose values may also be comma-separated.
type setAnswersList []string

func (l *setAnswersList) String() string { return strings.Join(*l, ",") }
func (l *setAnswersList) Set(v string) error {
	for _, k := range strings.Split(v, ",") {
		if k = strings.TrimSpace(k); k != "" {
			*l = append(*l, k)
		}
	}
	return nil
}

func scopeList(sm config.ScopeMigration) []any {
	out := []any{}
	for _, c := range sm.Changes {
		out = append(out, knObj("key", c.Key, "action", c.Action, "reason", c.Reason, "before", c.Before, "after", c.After))
	}
	return out
}

// scopeLine is one scope change as text: `delete <key> (<reason>; <before> -> <after>)`
// or `move <key> -> config.local.json (<before> -> <after>)`.
func scopeLine(c config.ScopeChange) string {
	b, _ := jsonx.MarshalCompact(c.Before)
	a, _ := jsonx.MarshalCompact(c.After)
	if c.Action == config.ScopeMove {
		return fmt.Sprintf("move %s -> config.local.json (%s -> %s)", c.Key, b, a)
	}
	return fmt.Sprintf("delete %s (%s; %s -> %s)", c.Key, c.Reason, b, a)
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
	var ae *config.AccountConflictError
	var me *config.MergeConflictError
	switch {
	case errors.As(err, &me):
		list := []any{}
		for _, x := range me.Conflicts {
			list = append(list, knObj("file", x.File, "old", x.Old, "new", x.New))
		}
		d := knObj("blockedBy", "merge conflict", "conflicts", list, "changed", false)
		return Result{Data: d}, Refused("%s", err.Error())
	case errors.As(err, &ae):
		list := []any{}
		for _, x := range ae.Conflicts {
			list = append(list, knObj("file", x.File, "name", x.Name, "dirs", anySlice([]string{x.A, x.B})))
		}
		d := knObj("blockedBy", "account conflict", "conflicts", list, "changed", false)
		return Result{Data: d}, Refused("%s", err.Error())
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
	old := config.LegacyRoundKeys(c.deps().Config(root))
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
