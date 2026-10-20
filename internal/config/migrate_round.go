package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
)

// Rename is one round-only key that moved out of work.* (#1097).
type Rename struct{ Old, New string }

// RoundKeyRenames are the keys only rounds read, which lived under work.*
// until #1097. The old names are not read any more: `rota migrate config`,
// `config fill` and `rota update` rewrite them, and round verbs refuse a
// config that still holds one (LegacyRoundKeys).
var RoundKeyRenames = []Rename{
	{"work.workerSlots", "round.slots"},
	{"work.dispatch", "round.dispatch"},
	{"work.workerCommand", "round.workerCommand"},
	{"work.operatorCommand", "round.operatorCommand"},
	{"work.accounts", "round.accounts"},
	{"work.codexAccounts", "round.codexAccounts"},
	{"work.codexCommand", "round.codexCommand"},
	{"work.opencodeAccounts", "round.opencodeAccounts"},
	{"work.opencodeCommand", "round.opencodeCommand"},
	{"work.envSetup", "round.envSetup"},
	{"work.portBase", "round.portBase"},
	{"work.portBlock", "round.portBlock"},
	{"work.herdrGroupSlots", "round.herdrGroupSlots"},
	{"work.itemTimeoutMinutes", "round.itemTimeoutMinutes"},
}

// RenamedRoundKey is the new name of an old work.* round key, "" for any
// other name.
func RenamedRoundKey(old string) string {
	for _, r := range RoundKeyRenames {
		if r.Old == old {
			return r.New
		}
	}
	return ""
}

// Conflict is an old and a new name both set, to different values, in one
// file. The migration refuses it rather than drop either value.
type Conflict struct {
	File     string // config.json or config.local.json
	Old, New string
	OldValue any
	NewValue any
}

// ConflictError is MigrateRoundKeysFiles' refusal: nothing was written.
type ConflictError struct{ Conflicts []Conflict }

func (e *ConflictError) Error() string {
	var parts []string
	for _, c := range e.Conflicts {
		ov, _ := jsonx.MarshalCompact(c.OldValue)
		nv, _ := jsonx.MarshalCompact(c.NewValue)
		parts = append(parts, fmt.Sprintf("%s: %s is %s but %s is %s", c.File, c.Old, ov, c.New, nv))
	}
	return "old and new round keys disagree, nothing written (" + strings.Join(parts, "; ") + "); unset one of each pair and re-run"
}

// corruptFile is ErrCorrupt for a named config file, so a caller matching
// ErrCorrupt still matches a malformed config.local.json.
type corruptFile struct{ name string }

func (e corruptFile) Error() string        { return ".rota/" + e.name + " is not a valid JSON object" }
func (e corruptFile) Is(target error) bool { return target == ErrCorrupt }

// MigrateRoundKeys moves every RoundKeyRenames key present in doc to its new
// name, value unchanged, and returns the resulting object and the moves in
// table order. When the new name already holds an equal value the old one is
// just dropped; when it holds a different one that is a conflict, and doc is
// returned untouched with every conflict listed. An emptied work object goes.
// Running it on its own output changes nothing.
func MigrateRoundKeys(doc *jsonx.Object) (*jsonx.Object, []Rename, []Conflict) {
	var moved []Rename
	var conflicts []Conflict
	for _, r := range RoundKeyRenames {
		ov, ok := lookupPresent(doc, strings.Split(r.Old, "."))
		if !ok {
			continue
		}
		if nv, set := lookupPresent(doc, strings.Split(r.New, ".")); set && !sameJSON(ov, nv) {
			conflicts = append(conflicts, Conflict{Old: r.Old, New: r.New, OldValue: ov, NewValue: nv})
			continue
		}
		moved = append(moved, r)
	}
	if len(conflicts) > 0 {
		return doc, nil, conflicts
	}
	for _, r := range moved {
		segs := strings.Split(r.Old, ".")
		ov, _ := lookupPresent(doc, segs)
		if _, set := lookupPresent(doc, strings.Split(r.New, ".")); !set {
			doc = fillKey(doc, "", strings.Split(r.New, "."), ov)
		}
		work, _ := getObject(doc, segs[0])
		work.Delete(segs[1])
	}
	if work, ok := getObject(doc, "work"); ok && len(moved) > 0 && len(work.Keys()) == 0 {
		doc.Delete("work")
	}
	return doc, moved, nil
}

func sameJSON(a, b any) bool {
	x, err1 := jsonx.MarshalCompact(a)
	y, err2 := jsonx.MarshalCompact(b)
	return err1 == nil && err2 == nil && bytes.Equal(x, y)
}

// RoundMigration is what MigrateRoundKeysFiles found, per file.
type RoundMigration struct {
	Config []Rename // moves in .rota/config.json
	Local  []Rename // moves in .rota/config.local.json
}

// Changed reports whether any file holds an old name.
func (m RoundMigration) Changed() bool { return len(m.Config) > 0 || len(m.Local) > 0 }

// MigrateRoundKeysFiles runs MigrateRoundKeys on root's .rota/config.json and
// .rota/config.local.json and, when apply is set, writes the files that
// changed, under their locks. Both files are read and planned before either
// is written: a malformed file (ErrCorrupt naming it) or a conflict
// (*ConflictError) writes nothing. config.local.json is written first; each
// file migrates on its own, so a run cut short between the two writes is
// finished by the next. A missing file, and an empty config.local.json, have
// nothing to migrate.
func MigrateRoundKeysFiles(root string, apply bool) (RoundMigration, error) {
	var m RoundMigration
	path, local := configPath(root), localPath(root)
	err := fsio.Locked(path, fsio.LockTimeout, func() error {
		if _, err := os.Stat(local); err != nil {
			return migrateRoundPlan(path, "", &m, apply)
		}
		return fsio.Locked(local, fsio.LockTimeout, func() error {
			return migrateRoundPlan(path, local, &m, apply)
		})
	})
	return m, err
}

// migrateRoundPlan is MigrateRoundKeysFiles with the locks held; local is ""
// when there is no config.local.json.
func migrateRoundPlan(path, local string, m *RoundMigration, apply bool) error {
	cfg, err := readMigrateTarget(path, "config.json")
	if err != nil {
		return err
	}
	loc, err := readMigrateTarget(local, "config.local.json")
	if err != nil {
		return err
	}
	var all []Conflict
	var cfgOut, locOut *jsonx.Object
	if loc != nil {
		var c []Conflict
		locOut, m.Local, c = MigrateRoundKeys(loc)
		for _, x := range c {
			x.File = "config.local.json"
			all = append(all, x)
		}
	}
	if cfg != nil {
		var c []Conflict
		cfgOut, m.Config, c = MigrateRoundKeys(cfg)
		for _, x := range c {
			x.File = "config.json"
			all = append(all, x)
		}
	}
	if len(all) > 0 {
		*m = RoundMigration{}
		return &ConflictError{Conflicts: all}
	}
	if !apply {
		return nil
	}
	if len(m.Local) > 0 {
		if err := fsio.WriteJSONAtomic(local, locOut); err != nil {
			return err
		}
	}
	if len(m.Config) > 0 {
		return fsio.WriteJSONAtomic(path, cfgOut)
	}
	return nil
}

// readMigrateTarget reads one config layer for the migration: nil for a
// missing file (or path ""), and for an empty or blank config.local.json, which
// Load already treats as absent; ErrCorrupt naming the file for anything that
// is not a JSON object.
func readMigrateTarget(path, name string) (*jsonx.Object, error) {
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, corruptFile{name}
	}
	if name == "config.local.json" && len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	doc, ok := decodeObject(raw)
	if !ok {
		return nil, corruptFile{name}
	}
	return doc, nil
}

// LegacyRoundKeys lists, in RoundKeyRenames order, the old work.* round keys
// the merged config cfg (both layers, as Load returns it) still holds. Nothing
// reads them, so a round verb refuses to run while any is listed.
func LegacyRoundKeys(cfg any) []string {
	var out []string
	for _, r := range RoundKeyRenames {
		if _, ok := Lookup(cfg, r.Old); ok {
			out = append(out, r.Old)
		}
	}
	return out
}
