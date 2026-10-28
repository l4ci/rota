package config

import (
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/projects"
)

// The scope step of `rota migrate config` (#1786): keys that belong outside the
// tracked .rota/config.json leave it.

// Scope migration actions.
const (
	ScopeDelete = "delete"
	ScopeMove   = "move"
)

// ScopeChange is one key the scope step deletes from .rota/config.json or
// moves to config.local.json. Before and After are the key's effective value
// (all layers merged) before and after the change; Reason says why.
type ScopeChange struct {
	Key, Action, Reason string
	Before, After       any
}

// ScopeMigration is what MigrateScopeFiles found, in schema order.
type ScopeMigration struct{ Changes []ScopeChange }

// Changed reports whether the step has anything to do.
func (m ScopeMigration) Changed() bool { return len(m.Changes) > 0 }

// MigrateScopeFiles plans, and with apply writes, the scope step on root's
// config files. Every machine key .rota/config.json holds goes: it is deleted
// when config.local.json already holds the key (the local value wins), when it
// equals the global value or when it equals the schema default; otherwise it
// moves to config.local.json with its value. Every taste key the tracked file
// pins at its schema default is deleted. A key named in keep, or under a named object key, stays where it
// is. Under apply config.local.json is written first (created holding only the
// moved keys when it is missing), then config.json, so a run cut short is
// finished by the next; a second run plans nothing. A malformed file is
// ErrCorrupt naming it, and nothing is written. The step plans on the files as
// the round-key rename (MigrateRoundKeys) leaves them, so a preview that also
// renames old work.* names lists what --apply, which renames first, does.
func MigrateScopeFiles(env projects.Env, root string, apply bool, keep []string) (ScopeMigration, error) {
	var m ScopeMigration
	path, local := configPath(root), localPath(root)
	err := fsio.Locked(path, fsio.LockTimeout, func() error {
		return fsio.Locked(local, fsio.LockTimeout, func() error {
			return migrateScopePlan(env, path, local, keep, &m, apply)
		})
	})
	return m, err
}

func migrateScopePlan(env projects.Env, path, local string, keep []string, m *ScopeMigration, apply bool) error {
	cfg, err := readMigrateTarget(path, "config.json")
	if err != nil || cfg == nil {
		return err
	}
	loc, err := readMigrateTarget(local, "config.local.json")
	if err != nil {
		return err
	}
	// The view the earlier steps leave: the work.* rename and the merges step.
	// Under apply they already ran, so this is a no-op; in a preview it makes
	// the plan the one --apply writes. A merges conflict leaves a file as it is:
	// the merges step reports it.
	global := globalLayer(env)
	var below []any
	if h, ok := walk(global, "round.harnesses"); ok {
		below = asList(h)
	}
	cfg, _, _ = MigrateRoundKeys(cfg)
	if out, _, c := migrateMergesDoc(cfg, below); len(c) == 0 {
		cfg = out
	}
	if h, _ := lookupPresent(cfg, []string{"round", "harnesses"}); len(asList(h)) > 0 {
		below = asList(h)
	}
	l := layers{global: global, project: cloneObject(cfg)}
	if loc != nil {
		loc, _, _ = MigrateRoundKeys(loc)
		if out, _, c := migrateMergesDoc(loc, below); len(c) == 0 {
			loc = out
		}
		l.local = cloneObject(loc)
	}
	merged := mergeLayers(&l)
	moves := false
	for _, k := range scopeRows(cfg) {
		v, ok := walk(cfg, k.Name)
		if !ok || keptKey(keep, k.Name) || k.Scope == ScopeProject {
			continue
		}
		before, _ := effectiveValue(merged, k)
		// What the key reads as once the tracked copy is gone.
		inherited := Default(k)
		g, hasGlobal := walk(l.global, k.Name)
		if hasGlobal {
			inherited = g
		}
		var lv any
		hasLocal := false
		if loc != nil {
			lv, hasLocal = walk(loc, k.Name)
		}
		if hasLocal {
			inherited = lv
		}
		c := ScopeChange{Key: k.Name, Action: ScopeDelete, Before: before, After: inherited}
		switch {
		case k.Scope == ScopeTaste:
			if !sameJSON(v, Default(k)) {
				continue
			}
			c.Reason = "pinned at its default"
		case hasLocal && k.Name == "round.accounts" && !sameJSON(mergeByKind([][]any{asList(v), asList(lv)}), lv):
			// Accounts merge by kind: kinds the local list lacks move into it.
			c.Action, c.Reason, c.After = ScopeMove, "machine key", mergeByKind([][]any{asList(v), asList(lv)})
			moves = true
		case hasLocal:
			c.Reason = "config.local.json already holds it"
		case hasGlobal && sameJSON(v, g):
			c.Reason = "equals global"
		case sameJSON(v, Default(k)):
			c.Reason = "equals default"
		default:
			c.Action, c.Reason, c.After = ScopeMove, "machine key", v
			moves = true
		}
		m.Changes = append(m.Changes, c)
	}
	if !apply || !m.Changed() {
		return nil
	}
	if moves {
		out := loc
		if out == nil {
			out = jsonx.NewObject()
		}
		for _, c := range m.Changes {
			if c.Action == ScopeMove {
				putIn(out, strings.Split(c.Key, "."), c.After)
			}
		}
		if err := fsio.WriteJSONAtomic(local, out); err != nil {
			return err
		}
	}
	for _, c := range m.Changes {
		deleteIn(cfg, strings.Split(c.Key, "."))
	}
	return fsio.WriteJSONAtomic(path, cfg)
}

// keptKey reports whether keep names the key or an object key above it, so
// --keep round.commands holds every round.commands.<kind> entry.
func keptKey(keep []string, name string) bool {
	for _, k := range keep {
		if name == k || strings.HasPrefix(name, k+".") {
			return true
		}
	}
	return false
}

// scopeRows is the schema rows the scope step walks. An object-valued row
// (round.commands) is replaced by one row per entry the tracked file holds, so
// the object moves entry by entry: an entry config.local.json lacks moves, one
// it holds is dropped, and the others survive beside it.
func scopeRows(cfg *jsonx.Object) []Key {
	var rows []Key
	for _, k := range Keys {
		if k.Type != TypeObject {
			rows = append(rows, k)
			continue
		}
		obj, ok := walk(cfg, k.Name)
		o, isObj := obj.(*jsonx.Object)
		if !ok || !isObj {
			rows = append(rows, k)
			continue
		}
		for _, name := range o.Keys() {
			e := k
			e.Name, e.Type, e.Default = k.Name+"."+name, TypeString, ""
			rows = append(rows, e)
		}
	}
	return rows
}

// cloneObject is a deep copy of o, for mergeLayers to edit without touching
// the documents the step writes.
func cloneObject(o *jsonx.Object) *jsonx.Object {
	b, err := jsonx.Marshal(o)
	if err != nil {
		return jsonx.NewObject()
	}
	c, _ := decodeObject(b)
	return c
}

func compactJSON(v any) string {
	b, err := jsonx.MarshalCompact(v)
	if err != nil {
		return ""
	}
	return string(b)
}
