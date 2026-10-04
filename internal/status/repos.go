package status

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/repos"
)

// Repo is one registered sub-repo: its name and absolute, symlink-resolved path.
type Repo struct{ Name, Path string }

// LoadRepos reads <base>/.rota/repos.json through internal/repos, the one
// parser of that file (hvlib_repos.load_repos semantics).
func LoadRepos(base string) []Repo {
	var out []Repo
	for _, r := range repos.Load(base) {
		out = append(out, Repo{Name: r.Name, Path: r.Path})
	}
	return out
}

// Realpath is os.path.realpath (repos.Realpath).
func Realpath(p string) string { return repos.Realpath(p) }

// ParseReposCSV is parse_repos_csv: comma-separated names, stripped, empties
// dropped, duplicates kept in order.
func ParseReposCSV(csv string) []string {
	var out []string
	for _, p := range strings.Split(csv, ",") {
		if p = pystr.Strip(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Missing returns the names in names that repos does not register.
func Missing(repos []Repo, names []string) []string {
	have := map[string]bool{}
	for _, r := range repos {
		have[r.Name] = true
	}
	var out []string
	for _, n := range names {
		if !have[n] {
			out = append(out, n)
		}
	}
	return out
}

// HasCode is whether dir holds anything besides the umbrella's own files and
// the top-level directories of the given sub-repos (hv-refactor-targets).
func HasCode(dir string, repos []Repo) bool {
	real := Realpath(dir)
	sub := map[string]bool{}
	for _, r := range repos {
		rel, err := filepath.Rel(real, r.Path)
		if err != nil || rel == "" || strings.HasPrefix(rel, "..") {
			continue
		}
		sub[strings.SplitN(rel, string(filepath.Separator), 2)[0]] = true
	}
	ignore := map[string]bool{".git": true, ".rota": true, ".claude": true, ".claude-plugin": true,
		".gitignore": true, ".docsignore": true, ".stow-local-ignore": true, ".DS_Store": true}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !ignore[e.Name()] && !sub[e.Name()] {
			return true
		}
	}
	return false
}
