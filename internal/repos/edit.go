package repos

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/rotatree"
)

var (
	// ErrNotRepo: the path is missing, not a directory, or has no .git entry.
	ErrNotRepo = errors.New("not a git repo")
	// ErrOutside: the path is not below the umbrella root.
	ErrOutside = errors.New("not below the umbrella root")
	// ErrConflict: the name or the path is already registered for another entry.
	ErrConflict = errors.New("already registered")
	// ErrUnknown: no registered sub-repo has the name.
	ErrUnknown = errors.New("not registered")
)

// Add registers the git repo at path under name, which defaults to the
// directory's basename. path may be absolute or relative to the working
// directory and must lie strictly below root. Registering an entry that is
// already there (same name, same directory) changes nothing; the same name for
// another directory, or the same directory under another name, is ErrConflict.
// The file stays sorted by name and entries it holds that Add does not know
// are kept as they are.
func Add(root, path, name string) (r Repo, changed bool, err error) {
	abs := Realpath(path)
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return Repo{}, false, fmt.Errorf("%w: %s is not a directory", ErrNotRepo, path)
	}
	if _, err := os.Stat(filepath.Join(abs, ".git")); err != nil {
		return Repo{}, false, fmt.Errorf("%w: %s has no .git", ErrNotRepo, path)
	}
	rel, err := filepath.Rel(Realpath(root), abs)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return Repo{}, false, fmt.Errorf("%w: %s", ErrOutside, path)
	}
	if name == "" {
		name = filepath.Base(abs)
	}
	r = Repo{Name: name, Rel: "./" + filepath.ToSlash(rel), Path: abs}

	for _, have := range Load(root) {
		switch {
		case have.Name == name && have.Path == abs:
			return have, false, nil
		case have.Name == name:
			return Repo{}, false, fmt.Errorf("%w: %s is registered at %s", ErrConflict, name, have.Rel)
		case have.Path == abs:
			return Repo{}, false, fmt.Errorf("%w: %s is registered as %s", ErrConflict, path, have.Name)
		}
	}
	entry := newEntry(name, r.Rel)
	if err := edit(root, false, func(entries []any) []any { return append(entries, entry) }); err != nil {
		return Repo{}, false, err
	}
	return r, true, nil
}

// Remove unregisters name and returns the entry it held. The repo itself is
// not touched. A name nothing registers is ErrUnknown.
func Remove(root, name string) (Repo, error) {
	var gone Repo
	for _, have := range Load(root) {
		if have.Name == name {
			gone = have
		}
	}
	if gone.Name == "" {
		return Repo{}, fmt.Errorf("%w: %s", ErrUnknown, name)
	}
	err := edit(root, false, func(entries []any) []any {
		var keep []any
		for _, e := range entries {
			if o, ok := e.(*jsonx.Object); ok {
				if n, _ := o.Get("name"); n == name {
					continue
				}
			}
			keep = append(keep, e)
		}
		return keep
	})
	return gone, err
}

// SetTopLevel makes names the registered top-level sub-repos: each gets a
// `./<name>` entry unless an entry of that name is already there (kept as it
// is, so a nested registration keeps its path), and an entry shaped
// `./<other>` for a name not in names is dropped as stale. Every other entry
// is kept. A registry that is not a JSON object is replaced, which is what
// re-running `rota init umbrella` over a corrupt file has always done.
func SetTopLevel(root string, names []string) error {
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	return edit(root, true, func(entries []any) []any {
		var out []any
		have := map[string]bool{}
		for _, e := range entries {
			n := entryName(e)
			if o, ok := e.(*jsonx.Object); ok && !want[n] {
				if p, _ := o.Get("path"); p == "./"+n {
					continue
				}
			}
			have[n] = true
			out = append(out, e)
		}
		for _, n := range names {
			if !have[n] {
				out = append(out, newEntry(n, "./"+n))
				have[n] = true
			}
		}
		return out
	})
}

func newEntry(name, rel string) *jsonx.Object {
	entry := jsonx.NewObject()
	entry.Set("name", name)
	entry.Set("path", rel)
	return entry
}

// edit rewrites repos.json with fn applied to its entry list, sorted by name.
// A registry that exists but is not a JSON object is an error rather than
// something to overwrite, unless replaceCorrupt.
func edit(root string, replaceCorrupt bool, fn func([]any) []any) error {
	path := rotatree.Repos(root)
	reg := jsonx.NewObject()
	if _, err := os.Stat(path); err == nil {
		o, ok := fsio.LoadJSON(path, nil).(*jsonx.Object)
		switch {
		case ok:
			reg = o
		case !replaceCorrupt:
			return fmt.Errorf("%s is not a valid JSON object", path)
		}
	}
	list, _ := reg.Get("repos")
	entries, _ := list.([]any)
	entries = fn(append([]any{}, entries...))
	if entries == nil {
		entries = []any{}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entryName(entries[i]) < entryName(entries[j]) })
	reg.Set("repos", entries)
	return fsio.WriteJSONAtomic(path, reg)
}

func entryName(e any) string {
	if o, ok := e.(*jsonx.Object); ok {
		if n, ok := o.Get("name"); ok {
			s, _ := n.(string)
			return s
		}
	}
	return ""
}
