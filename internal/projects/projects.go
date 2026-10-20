// Package projects is the machine-wide registry of rota projects: one
// projects.json under the rota state dir, $XDG_STATE_HOME/rota (default
// ~/.local/state/rota); it was under the config dir until #1184. `rota init`
// registers the project it seeds and `rota projects` lists them. It is not .rota/repos.json, the per-project
// umbrella sub-repo registry (internal/repos).
//
// A path that no longer exists is kept and flagged on read, never pruned by
// List or Register: a project on an unmounted drive comes back when the drive
// does. Only Cleanup, the explicit `rota projects cleanup`, drops them.
package projects

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/repos"
	"github.com/l4ci/rota/internal/rotatree"
)

// Project is one registered project: Path absolute and realpath-resolved, Name
// its directory name, LastSeen the UTC RFC 3339 time of the last registration.
type Project struct {
	Path, Name, LastSeen string
	Missing              bool
}

// Dir is the global rota config dir: $XDG_CONFIG_HOME/rota when that is an
// absolute path (the XDG spec ignores a relative one), else ~/.config/rota.
func Dir() (string, error) {
	if x := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(x) {
		return filepath.Join(x, "rota"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.New("no global config dir: neither XDG_CONFIG_HOME nor HOME is set")
	}
	return filepath.Join(home, ".config", "rota"), nil
}

// StateDir is the rota state dir, where machine-rewritten files live (the
// project registry and its lock): $XDG_STATE_HOME/rota when that is an
// absolute path, else ~/.local/state/rota.
func StateDir() (string, error) {
	if x := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(x) {
		return filepath.Join(x, "rota"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.New("no state dir: neither XDG_STATE_HOME nor HOME is set")
	}
	return filepath.Join(home, ".local", "state", "rota"), nil
}

const registryName = "projects.json"

// file is the registry path. The first call after an upgrade moves a registry
// left in the config dir (migrate).
func file() (string, error) {
	d, err := StateDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(d, registryName)
	if old, err := Dir(); err == nil {
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
		os.Remove(old)
		os.Remove(old + ".lock")
		return
	}
	if os.MkdirAll(filepath.Dir(path), 0o755) != nil {
		return
	}
	if os.Rename(old, path) != nil {
		// A different filesystem: copy, then drop the old file.
		b, err := os.ReadFile(old)
		if err != nil || fsio.WriteFileAtomic(path, b) != nil {
			return
		}
		os.Remove(old)
	}
	os.Remove(old + ".lock")
}

// Register adds root to the registry, or refreshes its lastSeen when it is
// already there. It is keyed by realpath, so a symlinked checkout does not
// register twice. added says whether the entry is new.
func Register(root string, now time.Time) (added bool, err error) {
	path, err := file()
	if err != nil {
		return false, err
	}
	abs := repos.Realpath(root)
	stamp := now.UTC().Format(time.RFC3339)
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
					o.Set("lastSeen", stamp)
					return doc, nil
				}
			}
		}
		added = true
		e := jsonx.NewObject()
		e.Set("path", abs)
		e.Set("name", filepath.Base(abs))
		e.Set("lastSeen", stamp)
		doc.Set("projects", append(items, e))
		return doc, nil
	})
	return added && err == nil, err
}

// List returns the registered projects sorted by name then path, with Missing
// set for a path that is gone. An absent or unreadable registry is empty.
func List() ([]Project, error) {
	path, err := file()
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

// Cleanup drops every entry whose directory is gone or no longer holds .rota/,
// under the registry lock, and returns what it removed (in registry order). An
// absent registry removes nothing and is not created.
func Cleanup() ([]Project, error) {
	path, err := file()
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
					if _, err := os.Stat(rotatree.Dir(ps)); err != nil {
						n, _ := o.Get("name")
						s, _ := o.Get("lastSeen")
						ns, _ := n.(string)
						ls, _ := s.(string)
						removed = append(removed, Project{Path: ps, Name: ns, LastSeen: ls, Missing: true})
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
func Remove(dir string) ([]Project, error) {
	path, err := file()
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
