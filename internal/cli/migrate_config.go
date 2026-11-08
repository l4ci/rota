package cli

import (
	"flag"
	"fmt"
	"github.com/l4ci/rota/internal/exitmap"
	"strings"

	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/jsonx"
)

func configMigrateCommand() *Command {
	return &Command{Name: "config", Summary: "move the round-only work.* keys to round.*, fold the codex and opencode account keys into round.accounts, merge and rename the keys #1825 folded together (models.*, the *Command keys, round.workerKind, issues.label), and move machine and default taste keys out of config.json (preview unless --apply)", Verb: migrateConfig}
}

// migrateConfig is `rota migrate config` (#1097): every round-only key under
// work.* moves to its round.* name in both config layers, value unchanged.
// An old and a new name with different values refuse (exit 4) and nothing is
// written. It is idempotent. `rota config fill` and `rota init` run the
// same migration. Then the accounts step (#1824, config.MigrateAccountsFiles) folds
// round.codexAccounts and round.opencodeAccounts into one round.accounts list
// in the global, tracked and local files. Then the merges step (#1825,
// config.MigrateMergesFiles) moves the merged and renamed keys (models.orchestrator,
// round.codexCommand, round.workerKind ...) to their new names; an old and a
// new key with different values refuse (exit 4). Then the scope step (#1786, config.MigrateScopeFiles) moves
// machine keys out of config.json and drops taste keys pinned at their
// default; --keep names keys it leaves in place. config.Migrate runs the steps
// in that order and plans them all before an --apply writes any.
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
		mig, err := config.Migrate(c.deps().projectsEnv(), root, *apply, keep)
		if err != nil {
			return roundMigrationFail(err)
		}
		m, am, mm, sm := mig.Round, mig.Accounts, mig.Merges, mig.Scope
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
	// The effective value usually stays put: say so once instead of b -> b.
	change := fmt.Sprintf("%s -> %s", b, a)
	if string(b) == string(a) {
		change = "stays " + string(a)
	}
	if c.Action == config.ScopeMove {
		return fmt.Sprintf("move %s -> config.local.json (%s)", c.Key, change)
	}
	return fmt.Sprintf("delete %s (%s; %s)", c.Key, c.Reason, change)
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
	data, out := exitmap.Translate(err, exitmap.Options{Classes: exitmap.ConfigRefusal | exitmap.ConfigCorrupt})
	if data != nil {
		return Result{Data: data}, out
	}
	return Result{}, out
}

// roundKeys refuses a round verb on a config that still names a
// round-only key under work.*, and warns on every other verb. Outside a
// project there is nothing to check.
func (p *preflight) roundKeys() (Result, error) {
	if p.g.RoundKeys == RoundKeysQuiet {
		return Result{}, nil
	}
	root, ok := p.project()
	if !ok {
		return Result{}, nil
	}
	c := p.c
	old := config.LegacyRoundKeys(p.config(root))
	if len(old) == 0 {
		return Result{}, nil
	}
	msg := "config still uses the old round key names " + strings.Join(old, ", ") + ", which nothing reads since they moved to round.*"
	if p.g.RoundKeys == RoundKeysRefuse {
		d := jsonx.NewObject()
		d.Set("blockedBy", blockedby.ConfigMigration)
		d.Set("keys", anySlice(old))
		d.Set("changed", false)
		return Result{Data: d}, Refused("%s", msg).WithHint("run: rota migrate config --apply")
	}
	c.Warn("%s; run: rota migrate config --apply", msg)
	return Result{}, nil
}
