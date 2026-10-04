// Package status owns .rota/status.json, the record of active work streams and
// the loop timestamp, and the sub-repo registry in .rota/repos.json. It matches
// hv-status-add, hv-status-remove, hv-status-repo-for, hv-resolve-handoff,
// hv-loop-stamp and hvlib_repos.load_repos.
//
// status.json is read and written as a jsonx tree, so keys the helpers do not
// know and the order of the ones they do survive a round trip, and a file rota
// writes is byte for byte what the Python helpers write.
package status

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/pystr"
)

// Now is the clock stamps are taken from; tests replace it.
var Now = time.Now

// Stamp is the timestamp format the helpers write: UTC, second precision,
// trailing "Z" (datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")).
func Stamp() string { return Now().UTC().Format("2006-01-02T15:04:05Z") }

// Path is the status.json of the project rooted at root.
func Path(root string) string { return filepath.Join(root, ".rota", "status.json") }

// Entry is one active work stream.
type Entry struct {
	Branch    string
	Repo      string // "" for a null or missing repo
	Items     []string
	Worktree  string // "" for null or missing
	StartedAt string
	Raw       *jsonx.Object
}

func str(o *jsonx.Object, key string) string {
	v, _ := o.Get(key)
	s, _ := v.(string)
	return s
}

// ActiveItems is active_items: the entry's "items" as a list, or the CSV
// string split and stripped. Anything else, and non-string elements, give
// nothing.
func ActiveItems(o *jsonx.Object) []string {
	raw, _ := o.Get("items")
	out := []string{}
	switch v := raw.(type) {
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	case string:
		for _, p := range strings.Split(v, ",") {
			if p = pystr.Strip(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

func entryOf(o *jsonx.Object) Entry {
	return Entry{
		Branch: str(o, "branch"), Repo: str(o, "repo"), Items: ActiveItems(o),
		Worktree: str(o, "worktree"), StartedAt: str(o, "startedAt"), Raw: o,
	}
}

// Entries lists the active entries of status.json; a missing or corrupt file,
// or an "active" that is not a list, gives none. Entries that are not
// objects are skipped.
func Entries(root string) []Entry {
	doc, ok := fsio.LoadJSON(Path(root), nil).(*jsonx.Object)
	if !ok {
		return nil
	}
	list, _ := doc.Get("active")
	arr, _ := list.([]any)
	var out []Entry
	for _, e := range arr {
		if o, ok := e.(*jsonx.Object); ok {
			out = append(out, entryOf(o))
		}
	}
	return out
}

// ErrShape is wrapped by the error for a status.json that parses but is not
// the {"active": [...]} document the helpers expect.
var ErrShape = errors.New("status.json has an unexpected shape")

func fresh() any {
	d := jsonx.NewObject()
	d.Set("active", []any{})
	return d
}

// update is a locked read-modify-write of status.json. fn edits the document
// and says whether to write it back; a missing or corrupt file starts as
// {"active": []}, as in the helpers.
func update(root string, fn func(doc *jsonx.Object) (write bool, err error)) error {
	path := Path(root)
	return fsio.Locked(path, fsio.LockTimeout, func() error {
		doc, ok := fsio.LoadJSON(path, fresh()).(*jsonx.Object)
		if !ok {
			return ErrShape
		}
		write, err := fn(doc)
		if err != nil || !write {
			return err
		}
		return fsio.WriteJSONAtomic(path, doc)
	})
}

// activeList is doc["active"] after setdefault("active", []).
func activeList(doc *jsonx.Object) ([]any, error) {
	v, ok := doc.Get("active")
	if !ok {
		doc.Set("active", []any{})
		return []any{}, nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, ErrShape
	}
	return arr, nil
}

func isEntry(v any, branch, repo string) bool {
	o, ok := v.(*jsonx.Object)
	if !ok {
		return false
	}
	return str(o, "branch") == branch && str(o, "repo") == repo
}

// Add records (branch, repo) as active with items, replacing an entry for the
// same pair. With ifAbsent an existing entry is left alone and changed is
// false. The new entry is {branch, repo, items, worktree, startedAt}, the
// helpers' key order, with null for an empty repo or worktree.
func Add(root, branch, repo string, items []string, worktree string, ifAbsent bool) (changed bool, err error) {
	err = update(root, func(doc *jsonx.Object) (bool, error) {
		arr, err := activeList(doc)
		if err != nil {
			return false, err
		}
		keep := make([]any, 0, len(arr)+1)
		for _, e := range arr {
			if isEntry(e, branch, repo) {
				if ifAbsent {
					return false, nil
				}
				continue
			}
			keep = append(keep, e)
		}
		e := jsonx.NewObject()
		e.Set("branch", branch)
		e.Set("repo", nullable(repo))
		e.Set("items", append([]string{}, items...))
		e.Set("worktree", nullable(worktree))
		e.Set("startedAt", Stamp())
		doc.Set("active", append(keep, e))
		changed = true
		return true, nil
	})
	return changed, err
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Remove deletes the entries for (branch, repo) and reports how many went.
// With repo "" only entries with no repo match: umbrella entries stay. Nothing
// is written when no entry matched, and a missing file is no work.
func Remove(root, branch, repo string) (removed int, err error) {
	if _, serr := os.Stat(Path(root)); serr != nil {
		return 0, nil
	}
	err = update(root, func(doc *jsonx.Object) (bool, error) {
		v, _ := doc.Get("active")
		arr, ok := v.([]any)
		if !ok {
			return false, nil
		}
		keep := make([]any, 0, len(arr))
		for _, e := range arr {
			if isEntry(e, branch, repo) {
				removed++
				continue
			}
			keep = append(keep, e)
		}
		if removed == 0 {
			return false, nil
		}
		doc.Set("active", keep)
		return true, nil
	})
	return removed, err
}

// Find is the first entry for branch. With repo given the entry's repo must
// match too; without it any repo does (hv-status-repo-for took the first
// entry by branch).
func Find(root, branch, repo string) (Entry, bool) {
	for _, e := range Entries(root) {
		if e.Branch == branch && (repo == "" || e.Repo == repo) {
			return e, true
		}
	}
	return Entry{}, false
}

// ---- handoff -----------------------------------------------------------------

// ErrEscapes is returned for a branch whose handoff path would leave
// .rota/handoff.
var ErrEscapes = errors.New("branch name escapes .rota/handoff")

// HandoffPath is the canonical handoff note path, relative to the project
// root with forward slashes: .rota/handoff/<branch>[@<repo>].md. A "/" in the
// branch stays literal.
func HandoffPath(branch, repo string) (string, error) {
	name := branch
	if repo != "" {
		name += "@" + repo
	}
	p := ".rota/handoff/" + name + ".md"
	rel, err := filepath.Rel(".rota/handoff", filepath.FromSlash(p))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", ErrEscapes
	}
	return p, nil
}

func isFile(root, rel string) bool {
	fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil && fi.Mode().IsRegular()
}

// Handoff resolves the handoff note of branch. Read mode (canonical false)
// probes: the repo-keyed note first, then the flat one, "" when neither is a
// file. Canonical mode returns the write path without probing. exists is
// whether the returned path is a file.
func Handoff(root, branch, repo string, canonical bool) (path string, exists bool, err error) {
	flat, err := HandoffPath(branch, "")
	if err != nil {
		return "", false, err
	}
	if repo != "" {
		keyed, err := HandoffPath(branch, repo)
		if err != nil {
			return "", false, err
		}
		if canonical {
			return keyed, isFile(root, keyed), nil
		}
		if isFile(root, keyed) {
			return keyed, true, nil
		}
	}
	if canonical {
		return flat, isFile(root, flat), nil
	}
	if isFile(root, flat) {
		return flat, true, nil
	}
	return "", false, nil
}

// RemoveHandoff deletes the canonical handoff note of (branch, repo), if any.
// handoffRemoved is whether a note was there and is gone.
func RemoveHandoff(root, branch, repo string) (removed bool, err error) {
	rel, err := HandoffPath(branch, repo)
	if err != nil {
		return false, err
	}
	p := filepath.Join(root, filepath.FromSlash(rel))
	if _, err := os.Lstat(p); err != nil {
		return false, nil
	}
	if err := os.Remove(p); err != nil {
		return false, err
	}
	return true, nil
}

// ---- loop stamp ---------------------------------------------------------------

func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case []any:
		return len(t) > 0
	case *jsonx.Object:
		return t.Len() > 0
	default: // json.Number
		b, _ := jsonx.MarshalCompact(t)
		s := string(b)
		return s != "0" && s != "0.0" && s != "-0" && s != "-0.0"
	}
}

// ErrLoopType is wrapped by the error for a loopStartedAt that is set but not
// a string.
var ErrLoopType = errors.New("loopStartedAt is not a string")

// LoopShow is the stored loopStartedAt, set false when it is unset or empty.
func LoopShow(root string) (stamp string, set bool, err error) {
	doc, ok := fsio.LoadJSON(Path(root), nil).(*jsonx.Object)
	if !ok {
		return "", false, nil
	}
	v, _ := doc.Get("loopStartedAt")
	if !truthy(v) {
		return "", false, nil
	}
	s, ok := v.(string)
	if !ok {
		return "", false, ErrLoopType
	}
	return s, true, nil
}

// LoopStart sets loopStartedAt to now unless it is already set (first write
// wins) and returns the stored value. changed is whether it was unset. The
// document is rewritten either way, with "active" added when missing, as
// hv-loop-stamp does.
func LoopStart(root string) (stamp string, changed bool, err error) {
	err = update(root, func(doc *jsonx.Object) (bool, error) {
		if _, err := activeList(doc); err != nil {
			return false, err
		}
		v, _ := doc.Get("loopStartedAt")
		if truthy(v) {
			s, ok := v.(string)
			if !ok {
				return false, ErrLoopType
			}
			stamp = s
			return true, nil
		}
		stamp, changed = Stamp(), true
		doc.Set("loopStartedAt", stamp)
		return true, nil
	})
	return stamp, changed, err
}

// LoopClear removes loopStartedAt. changed is whether a value was set. The
// document is rewritten either way.
func LoopClear(root string) (changed bool, err error) {
	err = update(root, func(doc *jsonx.Object) (bool, error) {
		if _, err := activeList(doc); err != nil {
			return false, err
		}
		v, _ := doc.Get("loopStartedAt")
		changed = truthy(v)
		doc.Delete("loopStartedAt")
		return true, nil
	})
	return changed, err
}
