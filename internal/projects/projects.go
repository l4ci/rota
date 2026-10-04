// Package projects is the machine-wide registry of rota projects: one
// projects.json under the global rota config dir, $XDG_CONFIG_HOME/rota
// (default ~/.config/rota). `rota init` registers the project it seeds and
// `rota projects` lists them. It is not .rota/repos.json, the per-project
// umbrella sub-repo registry (internal/repos).
//
// A path that no longer exists is kept and flagged on read, never pruned: a
// project on an unmounted drive comes back when the drive does.
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

func file() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "projects.json"), nil
}

// now is a seam for tests.
var now = func() time.Time { return time.Now().UTC() }

// Register adds root to the registry, or refreshes its lastSeen when it is
// already there. It is keyed by realpath, so a symlinked checkout does not
// register twice. added says whether the entry is new.
func Register(root string) (added bool, err error) {
	path, err := file()
	if err != nil {
		return false, err
	}
	abs := repos.Realpath(root)
	stamp := now().Format(time.RFC3339)
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
