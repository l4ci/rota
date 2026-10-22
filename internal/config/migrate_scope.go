package config

import (
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
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
// pins at its schema default is deleted. A key named in keep stays where it
// is. Under apply config.local.json is written first (created holding only the
// moved keys when it is missing), then config.json, so a run cut short is
// finished by the next; a second run plans nothing. A malformed file is
// ErrCorrupt naming it, and nothing is written.
func MigrateScopeFiles(root string, apply bool, keep []string) (ScopeMigration, error) {
	var m ScopeMigration
	path, local := configPath(root), localPath(root)
	err := fsio.Locked(path, fsio.LockTimeout, func() error {
		return fsio.Locked(local, fsio.LockTimeout, func() error {
			return migrateScopePlan(path, local, keep, &m, apply)
		})
	})
	return m, err
}

func migrateScopePlan(path, local string, keep []string, m *ScopeMigration, apply bool) error {
	cfg, err := readMigrateTarget(path, "config.json")
	if err != nil || cfg == nil {
		return err
	}
	loc, err := readMigrateTarget(local, "config.local.json")
	if err != nil {
		return err
	}
	merged, l := loadLayers(path)
	moves := false
	for _, k := range Keys {
		v, ok := walk(cfg, k.Name)
		if !ok || slices.Contains(keep, k.Name) || k.Scope == ScopeProject {
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
		case hasLocal:
			c.Reason = "config.local.json already holds " + compactJSON(lv)
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

func compactJSON(v any) string {
	b, err := jsonx.MarshalCompact(v)
	if err != nil {
		return ""
	}
	return string(b)
}
