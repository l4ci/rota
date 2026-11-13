// Package projects is the machine-wide registry of rota projects: one
// projects.json under the rota state dir, $XDG_STATE_HOME/rota (default
// ~/.local/state/rota); it was under the config dir until #1184. `rota init`
// registers projects outside temporary storage and `rota projects` lists them. It is not .rota/repos.json, the per-project
// umbrella sub-repo registry (internal/repos).
//
// A path that no longer exists is kept and flagged on read, never pruned by
// List or Register: a project on an unmounted drive comes back when the drive
// does. Only Cleanup, the explicit `rota projects cleanup`, drops them.
package projects

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/cfgdir"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/repos"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/stamp"
)

// Project is one registered project: Path absolute and realpath-resolved, Name
// its directory name, LastSeen the UTC RFC 3339 time of the last registration.
type Project struct {
	Path, Name, LastSeen string
	Missing              bool
}

// Temporary reports whether root is in temporary storage, resolving symlinks
// on both sides and matching whole path components (not just a string prefix).
func Temporary(env cfgdir.Env, root string) bool {
	dirs := []string{env.Getenv("TMPDIR"), "/tmp", "/var/folders", "/private/var/folders"}
	if env.TempDirs != nil {
		dirs = env.TempDirs()
	}
	root = repos.Realpath(root)
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		rel, err := filepath.Rel(repos.Realpath(dir), root)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

const registryName = "projects.json"

// file is the registry path. The first call after an upgrade moves a registry
// left in the config dir (migrate).
func file(env cfgdir.Env) (string, error) {
	d, err := cfgdir.StateDir(env)
	if err != nil {
		return "", err
	}
	path := filepath.Join(d, registryName)
	if old, err := cfgdir.Dir(env); err == nil {
		migrate(filepath.Join(old, registryName), path)
	}
	return path, nil
}

// migrate moves the pre-#1184 registry old to path, with its lock. A registry
// already in the state dir wins: the old copy is just removed. A failure
// leaves the old file in place and the move is retried on the next call.
func migrate(old, path string) {
	if _, err := os.Stat(old); err != nil {
		return
	}
	if _, err := os.Stat(path); err == nil {
		fsio.RemoveWithLock(old)
		return
	}
	if os.MkdirAll(filepath.Dir(path), 0o755) != nil {
		return
	}
	if fsio.MoveFilePreserveMode(old, path) != nil {
		return
	}
	fsio.RemoveWithLock(old)
}

// Register adds root to the registry, or refreshes its lastSeen when it is
// already there. It is keyed by realpath, so a symlinked checkout does not
// register twice. added says whether the entry is new.
func Register(env cfgdir.Env, root string, now time.Time) (added bool, err error) {
	path, err := file(env)
	if err != nil {
		return false, err
	}
	abs := repos.Realpath(root)
	at := stamp.Format(now)
	err = fsio.UpdateJSON(path, jsonx.NewObject(), func(v any) (any, error) {
		doc, ok := v.(*jsonx.Object)
		if !ok {
			doc = jsonx.NewObject()
		}
		raw, _ := doc.Get("projects")
		items, _ := raw.([]any)
		for _, it := range items {
			if o, ok := it.(*jsonx.Object); ok {
				if p, _ := o.Get("path"); p == abs {
					o.Set("lastSeen", at)
					return doc, nil
				}
			}
		}
		added = true
		e := jsonx.NewObject()
		e.Set("path", abs)
		e.Set("name", filepath.Base(abs))
		e.Set("lastSeen", at)
		doc.Set("projects", append(items, e))
		return doc, nil
	})
	return added && err == nil, err
}

// List returns the registered projects sorted by name then path, with Missing
// set for a path that is gone. An absent or unreadable registry is empty.
func List(env cfgdir.Env) ([]Project, error) {
	path, err := file(env)
	if err != nil {
		return nil, err
	}
	doc, _ := fsio.LoadJSON(path, nil).(*jsonx.Object)
	if doc == nil {
		return nil, nil
	}
	raw, _ := doc.Get("projects")
	items, _ := raw.([]any)
	var out []Project
	for _, it := range items {
		o, ok := it.(*jsonx.Object)
		if !ok {
			continue
		}
		p, _ := o.Get("path")
		n, _ := o.Get("name")
		s, _ := o.Get("lastSeen")
		ps, _ := p.(string)
		if ps == "" {
			continue
		}
		ns, _ := n.(string)
		ls, _ := s.(string)
		_, statErr := os.Stat(ps)
		out = append(out, Project{Path: ps, Name: ns, LastSeen: ls, Missing: statErr != nil})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}

// Cleanup drops every entry in temporary storage, gone or no longer holding .rota/,
// under the registry lock, and returns what it removed (in registry order). An
// absent registry removes nothing and is not created.
func Cleanup(env cfgdir.Env) ([]Project, error) {
	path, err := file(env)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err != nil {
		return nil, nil
	}
	var removed []Project
	err = fsio.UpdateJSON(path, jsonx.NewObject(), func(v any) (any, error) {
		doc, ok := v.(*jsonx.Object)
		if !ok {
			return v, nil
		}
		raw, _ := doc.Get("projects")
		items, _ := raw.([]any)
		kept := make([]any, 0, len(items))
		for _, it := range items {
			if o, ok := it.(*jsonx.Object); ok {
				p, _ := o.Get("path")
				if ps, _ := p.(string); ps != "" {
					_, statErr := os.Stat(rotatree.Dir(ps))
					if statErr != nil || Temporary(env, ps) {
						n, _ := o.Get("name")
						s, _ := o.Get("lastSeen")
						ns, _ := n.(string)
						ls, _ := s.(string)
						removed = append(removed, Project{Path: ps, Name: ns, LastSeen: ls, Missing: statErr != nil})
						continue
					}
				}
			}
			kept = append(kept, it)
		}
		if len(removed) == 0 {
			return doc, nil
		}
		doc.Set("projects", kept)
		return doc, nil
	})
	if err != nil {
		return nil, err
	}
	return removed, nil
}

// Remove drops the registry entry for dir, never the directory itself. dir is
// matched as given (made absolute) or by realpath, so a symlinked path or a
// directory that is already gone still finds its entry. removed is false when
// nothing matched. An absent registry removes nothing and is not created.
func Remove(env cfgdir.Env, dir string) ([]Project, error) {
	path, err := file(env)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err != nil {
		return nil, nil
	}
	var removed []Project
	abs, _ := filepath.Abs(dir)
	real := repos.Realpath(dir)
	err = fsio.UpdateJSON(path, jsonx.NewObject(), func(v any) (any, error) {
		doc, ok := v.(*jsonx.Object)
		if !ok {
			return v, nil
		}
		raw, _ := doc.Get("projects")
		items, _ := raw.([]any)
		kept := make([]any, 0, len(items))
		for _, it := range items {
			if o, ok := it.(*jsonx.Object); ok {
				p, _ := o.Get("path")
				if ps, _ := p.(string); ps != "" && (ps == abs || ps == real) {
					n, _ := o.Get("name")
					s, _ := o.Get("lastSeen")
					ns, _ := n.(string)
					ls, _ := s.(string)
					_, statErr := os.Stat(ps)
					removed = append(removed, Project{Path: ps, Name: ns, LastSeen: ls, Missing: statErr != nil})
					continue
				}
			}
			kept = append(kept, it)
		}
		if len(removed) == 0 {
			return doc, nil
		}
		doc.Set("projects", kept)
		return doc, nil
	})
	if err != nil {
		return nil, err
	}
	return removed, nil
}
