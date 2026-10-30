package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
)

// The accounts step of `rota migrate config` (#1824): the three per-harness
// account keys became one round.accounts list of {kind, name, dir}.

// oldAccountKeys are the per-kind keys round.accounts replaced, with the key
// each entry held its dir under. They join RemovedKeys: `config check` names
// them and `config fill` migrates, then deletes them. round.accounts itself
// stays, with entries that carried configDir and no kind (the Claude shape).
var oldAccountKeys = []struct{ Key, Kind, DirField string }{
	{"round.codexAccounts", KindCodex, "codexHome"},
	{"round.opencodeAccounts", KindOpencode, "configDir"},
}

// AccountChange is one config file the accounts step rewrites: From lists the
// keys it folded into round.accounts, N the entries the new list holds.
type AccountChange struct {
	File string // global config.json, config.json or config.local.json
	From []string
	N    int
}

// AccountsMigration is what MigrateAccountsFiles found, global file first.
type AccountsMigration struct{ Changes []AccountChange }

// Changed reports whether any file holds an old-shape account list.
func (m AccountsMigration) Changed() bool { return len(m.Changes) > 0 }

// AccountConflict is one name two entries of a file give to different dirs.
type AccountConflict struct{ File, Name, A, B string }

// AccountConflictError is MigrateAccountsFiles' refusal: nothing was written.
type AccountConflictError struct{ Conflicts []AccountConflict }

func (e *AccountConflictError) Error() string {
	var parts []string
	for _, c := range e.Conflicts {
		parts = append(parts, fmt.Sprintf("%s: %q is %q and %q", c.File, c.Name, c.A, c.B))
	}
	return "old account keys name one account with two dirs, nothing written (" + strings.Join(parts, "; ") + "); give them different names or one dir and re-run"
}

// migrateAccountsDoc folds doc's old account keys, and the Claude-shape
// entries of its round.accounts, into one new-shape round.accounts list, and
// returns the resulting object with the keys it folded. Layers merge
// round.accounts by kind (mergeAccounts), as the old per-kind keys layered, so
// doc carries only its own entries. Two entries with one name and different
// dirs are a conflict, and doc is returned untouched. Running it on its own
// output changes nothing.
func migrateAccountsDoc(doc *jsonx.Object) (*jsonx.Object, []string, []AccountConflict) {
	var from []string
	var own []Account
	raw, present := lookupPresent(doc, []string{"round", "accounts"})
	if present {
		own = accountEntries(raw)
		if list, _ := raw.([]any); legacyAccountShape(list) {
			from = append(from, "round.accounts")
		}
	}
	for _, o := range oldAccountKeys {
		v, ok := lookupPresent(doc, strings.Split(o.Key, "."))
		if !ok {
			continue
		}
		from = append(from, o.Key)
		list, _ := v.([]any)
		for _, e := range list {
			if obj, ok := e.(*jsonx.Object); ok {
				a := Account{Kind: o.Kind}
				if n, ok := obj.Get("name"); ok {
					a.Name, _ = n.(string)
				}
				if d, ok := obj.Get(o.DirField); ok {
					a.Dir, _ = d.(string)
				}
				own = append(own, a)
			}
		}
	}
	if len(from) == 0 {
		return doc, nil, nil
	}
	if c := accountConflicts(own); len(c) > 0 {
		return doc, nil, c
	}
	var list []any
	for _, a := range own {
		list = append(list, accountObject(a))
	}
	round, _ := getObject(doc, "round")
	if _, ok := round.Get("accounts"); ok || len(list) > 0 {
		if list == nil {
			list = []any{}
		}
		doc = fillKey(doc, "", []string{"round", "accounts"}, list)
	}
	round, _ = getObject(doc, "round")
	for _, o := range oldAccountKeys {
		round.Delete(strings.TrimPrefix(o.Key, "round."))
	}
	return doc, from, nil
}

func accountObject(a Account) *jsonx.Object {
	e := jsonx.NewObject()
	e.Set("kind", a.Kind)
	e.Set("name", a.Name)
	e.Set("dir", a.Dir)
	return e
}

// legacyAccountShape is whether a round.accounts list holds an entry the new
// shape would change: no kind, or the old configDir.
func legacyAccountShape(list []any) bool {
	for _, e := range list {
		if o, ok := e.(*jsonx.Object); ok {
			if _, hasKind := o.Get("kind"); !hasKind {
				return true
			}
			if _, old := o.Get("configDir"); old {
				return true
			}
		}
	}
	return false
}

func accountConflicts(own []Account) []AccountConflict {
	var out []AccountConflict
	seen := map[string]Account{}
	for _, a := range own {
		if a.Name == "" {
			continue
		}
		if first, ok := seen[a.Name]; !ok {
			seen[a.Name] = a
		} else if first.Dir != a.Dir {
			out = append(out, AccountConflict{Name: a.Name, A: first.Dir, B: a.Dir})
		}
	}
	return out
}

// accountFile is one config layer the step reads, lowest layer first.
type accountFile struct {
	name, path string
	global     bool // the global config layer, skipped when unusable rather than refused
	doc        *jsonx.Object
	changed    bool
}

// MigrateAccountsFiles runs the accounts step on the global config under
// globalDir ("" for none), root's .rota/config.json and its
// .rota/config.local.json. With apply it writes the files that changed, under
// their locks, lowest layer first. All three are read and planned before any is written:
// a malformed file or a conflict (*AccountConflictError) writes nothing. A
// missing file has nothing to migrate.
func MigrateAccountsFiles(root, globalDir string, apply bool) (AccountsMigration, error) {
	files := []*accountFile{{name: "config.json", path: configPath(root)}, {name: "config.local.json", path: localPath(root)}}
	if globalDir != "" {
		files = append([]*accountFile{{name: "global config.json", path: GlobalPath(globalDir), global: true}}, files...)
	}
	var m AccountsMigration
	var lock func(i int) error
	lock = func(i int) error {
		if i == len(files) {
			return migrateAccountsPlan(files, &m, apply)
		}
		if _, err := os.Stat(files[i].path); err != nil {
			return lock(i + 1)
		}
		return fsio.Locked(files[i].path, fsio.LockTimeout, func() error { return lock(i + 1) })
	}
	err := lock(0)
	return m, err
}

func migrateAccountsPlan(files []*accountFile, m *AccountsMigration, apply bool) error {
	for _, f := range files {
		doc, err := readAccountTarget(f)
		if err != nil {
			return err
		}
		f.doc = doc
	}
	var conflicts []AccountConflict
	for _, f := range files {
		if f.doc == nil {
			continue
		}
		if !f.global {
			// The renamed view, as the scope step plans on.
			f.doc, _, _ = MigrateRoundKeys(f.doc)
		}
		out, from, c := migrateAccountsDoc(f.doc)
		for _, x := range c {
			x.File = f.name
			conflicts = append(conflicts, x)
		}
		if len(from) > 0 {
			f.doc, f.changed = out, true
			m.Changes = append(m.Changes, AccountChange{File: f.name, From: from, N: len(Accounts(f.doc))})
		}
	}
	if len(conflicts) > 0 {
		*m = AccountsMigration{}
		return &AccountConflictError{Conflicts: conflicts}
	}
	if !apply {
		return nil
	}
	for _, f := range files {
		if f.changed {
			if err := fsio.WriteJSONAtomic(f.path, f.doc); err != nil {
				return err
			}
		}
	}
	return nil
}

// readAccountTarget reads one layer: nil when the file is missing (or, for
// config.local.json, blank), an error naming the file when it is not a JSON
// object. The global config is skipped when unusable, as Load skips it
// (GlobalError reports why): a step that runs inside `config fill` must not
// fail on a file it cannot see.
func readAccountTarget(f *accountFile) (*jsonx.Object, error) {
	if !f.global {
		return readMigrateTarget(f.path, f.name)
	}
	return readObject(f.path), nil
}
