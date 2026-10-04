// Package repos reads the umbrella sub-repo registry, .rota/repos.json. It is
// the one parser of that file, a port of hvlib_repos.load_repos: an entry
// counts when it has a non-empty string name and path, paths are relative to
// the project root and resolve like os.path.realpath, and a repeated name
// keeps its first position and takes the last path, as a Python dict does.
package repos

import (
	"path/filepath"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
)

// Repo is one registered sub-repo: Rel as written in repos.json, Path
// absolute and realpath-resolved.
type Repo struct {
	Name, Rel, Path string
}

// Load returns root's registered sub-repos in file order. A missing or
// unreadable registry is empty.
func Load(root string) []Repo {
	reg, ok := fsio.LoadJSON(filepath.Join(root, ".rota", "repos.json"), nil).(*jsonx.Object)
	if !ok {
		return nil
	}
	entries, _ := reg.Get("repos")
	items, _ := entries.([]any)
	var out []Repo
	at := map[string]int{}
	for _, e := range items {
		obj, ok := e.(*jsonx.Object)
		if !ok {
			continue
		}
		name, _ := obj.Get("name")
		rel, _ := obj.Get("path")
		n, _ := name.(string)
		r, _ := rel.(string)
		if n == "" || r == "" {
			continue
		}
		p := r
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		repo := Repo{Name: n, Rel: r, Path: Realpath(p)}
		if i, seen := at[n]; seen {
			out[i] = repo
			continue
		}
		at[n] = len(out)
		out = append(out, repo)
	}
	return out
}

// Paths is Load as name to absolute path.
func Paths(root string) map[string]string {
	out := map[string]string{}
	for _, r := range Load(root) {
		out[r.Name] = r.Path
	}
	return out
}

// Realpath is os.path.realpath: an absolute path with symlinks resolved as far
// as the path exists, and the rest kept as written.
func Realpath(p string) string {
	p, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	rest := ""
	for cur := p; ; {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(real, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}
