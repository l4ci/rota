package config

import (
	"fmt"
	"os"
	"reflect"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
)

// The merges step of `rota migrate config` (#1825): keys that merged into or
// were renamed to another key. Each moves its value unchanged; the old keys
// are in RemovedKeys, so `config check` names them and `config fill` migrates
// before it strips.

// mergeMoves are the plain old -> new value moves.
var mergeMoves = []struct{ Old, New string }{
	{"models.orchestrator", "orchestrator.model"},
	{"round.operatorCommand", "orchestrator.command"},
	{"models.worker", "round.tiers.claude.standard"},
	{"round.workerCommand", "round.commands.claude"},
	{"round.codexCommand", "round.commands.codex"},
	{"round.opencodeCommand", "round.commands.opencode"},
}

const (
	mergeIssuesLabel = "issues.label"
	mergeWorkerKind  = "round.workerKind"
)

// MergeChange is one config file the merges step rewrites: From lists the old
// keys it moved or dropped.
type MergeChange struct {
	File string // global config.json, config.json or config.local.json
	From []string
}

// MergesMigration is what MigrateMergesFiles found, global file first.
type MergesMigration struct{ Changes []MergeChange }

// Changed reports whether any file holds an old key.
func (m MergesMigration) Changed() bool { return len(m.Changes) > 0 }

// MergeConflict is one old/new key pair a file sets to different values.
type MergeConflict struct{ File, Old, New string }

// MergeConflictError is MigrateMergesFiles' refusal: nothing was written.
type MergeConflictError struct{ Conflicts []MergeConflict }

func (e *MergeConflictError) Error() string {
	var parts []string
	for _, c := range e.Conflicts {
		parts = append(parts, fmt.Sprintf("%s: %s and %s differ", c.File, c.Old, c.New))
	}
	return "old and new config keys disagree, nothing written (" + strings.Join(parts, "; ") + "); keep one value in each file and re-run"
}

// migrateMergesDoc moves doc's old keys to their new names and returns the
// resulting object with the old keys it handled. below is the harnesses list
// the layers under doc supply, for a workerKind in a file with no harnesses of
// its own. Old and new both set to different values is a conflict, and doc is
// returned untouched. Running it on its own output changes nothing.
func migrateMergesDoc(doc *jsonx.Object, below []any) (*jsonx.Object, []string, []MergeConflict) {
	var from []string
	var conflicts []MergeConflict
	type set struct {
		path string
		val  any
	}
	var sets []set
	var drops []string
	for _, mv := range mergeMoves {
		old, ok := lookupPresent(doc, strings.Split(mv.Old, "."))
		if !ok {
			continue
		}
		from = append(from, mv.Old)
		drops = append(drops, mv.Old)
		cur, has := lookupPresent(doc, strings.Split(mv.New, "."))
		switch {
		case !has:
			sets = append(sets, set{mv.New, old})
		case !reflect.DeepEqual(cur, old):
			conflicts = append(conflicts, MergeConflict{Old: mv.Old, New: mv.New})
		}
	}
	if old, ok := lookupPresent(doc, strings.Split(mergeIssuesLabel, ".")); ok {
		from = append(from, mergeIssuesLabel)
		drops = append(drops, mergeIssuesLabel)
		if _, has := lookupPresent(doc, []string{"issues", "labels", "inProgress"}); !has {
			sets = append(sets, set{"issues.labels.inProgress", old})
		}
	}
	if kind, ok := lookupPresent(doc, strings.Split(mergeWorkerKind, ".")); ok {
		from = append(from, mergeWorkerKind)
		drops = append(drops, mergeWorkerKind)
		own, _ := lookupPresent(doc, []string{"round", "harnesses"})
		list, _ := own.([]any)
		switch {
		case len(list) == 0:
			out := []any{kind}
			for _, h := range below {
				if h != kind {
					out = append(out, h)
				}
			}
			sets = append(sets, set{"round.harnesses", out})
		case !reflect.DeepEqual(list[0], kind):
			conflicts = append(conflicts, MergeConflict{Old: mergeWorkerKind, New: "round.harnesses"})
		}
	}
	if len(from) == 0 {
		return doc, nil, nil
	}
	if len(conflicts) > 0 {
		return doc, nil, conflicts
	}
	for _, s := range sets {
		doc = fillKey(doc, "", strings.Split(s.path, "."), s.val)
	}
	for _, d := range drops {
		deletePath(doc, strings.Split(d, "."))
	}
	return doc, from, nil
}

// deletePath removes segs from doc and any parent object that ends up empty.
func deletePath(doc *jsonx.Object, segs []string) {
	var chain []*jsonx.Object
	cur := doc
	for _, s := range segs[:len(segs)-1] {
		next, ok := getObject(cur, s)
		if !ok {
			return
		}
		chain = append(chain, cur)
		cur = next
	}
	cur.Delete(segs[len(segs)-1])
	for i := len(chain) - 1; i >= 0 && len(cur.Keys()) == 0; i-- {
		chain[i].Delete(segs[i])
		cur = chain[i]
	}
}

// MigrateMergesFiles runs the merges step on the global config under
// globalDir ("" for none), root's .rota/config.json and its
// .rota/config.local.json, lowest layer first so each file sees the harnesses
// the ones under it supply. With apply it writes the files that changed, under
// their locks. All three are read and planned before any is written: a
// malformed file or a conflict (*MergeConflictError) writes nothing. A missing
// file has nothing to migrate.
func MigrateMergesFiles(root, globalDir string, apply bool) (MergesMigration, error) {
	files := []*accountFile{{name: "config.json", path: configPath(root)}, {name: "config.local.json", path: localPath(root)}}
	if globalDir != "" {
		files = append([]*accountFile{{name: globalAccountsFile, path: GlobalPath(globalDir)}}, files...)
	}
	var m MergesMigration
	var lock func(i int) error
	lock = func(i int) error {
		if i == len(files) {
			return migrateMergesPlan(files, &m, apply)
		}
		if _, err := os.Stat(files[i].path); err != nil {
			return lock(i + 1)
		}
		return fsio.Locked(files[i].path, fsio.LockTimeout, func() error { return lock(i + 1) })
	}
	err := lock(0)
	return m, err
}

func migrateMergesPlan(files []*accountFile, m *MergesMigration, apply bool) error {
	for _, f := range files {
		doc, err := readAccountTarget(f)
		if err != nil {
			return err
		}
		f.doc = doc
	}
	var conflicts []MergeConflict
	var below []any
	for _, f := range files {
		if f.doc == nil {
			continue
		}
		view := f.doc
		if f.name != globalAccountsFile {
			// The renamed view, as the accounts step plans on.
			view, _, _ = MigrateRoundKeys(f.doc)
		}
		out, from, c := migrateMergesDoc(view, below)
		for _, x := range c {
			x.File = f.name
			conflicts = append(conflicts, x)
		}
		if len(from) > 0 && len(c) == 0 {
			f.doc, f.changed = out, true
			m.Changes = append(m.Changes, MergeChange{File: f.name, From: from})
		}
		if h, _ := lookupPresent(f.doc, []string{"round", "harnesses"}); len(asList(h)) > 0 {
			below = asList(h)
		}
	}
	if len(conflicts) > 0 {
		*m = MergesMigration{}
		return &MergeConflictError{Conflicts: conflicts}
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
