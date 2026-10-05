package status

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/repos"
	"github.com/l4ci/rota/internal/rotatree"
)

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

// Missing returns the names in names that registry does not register.
func Missing(registry []repos.Repo, names []string) []string {
	have := map[string]bool{}
	for _, r := range registry {
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
func HasCode(dir string, registry []repos.Repo) bool {
	real := repos.Realpath(dir)
	sub := map[string]bool{}
	for _, r := range registry {
		rel, err := filepath.Rel(real, r.Path)
		if err != nil || rel == "" || strings.HasPrefix(rel, "..") {
			continue
		}
		sub[strings.SplitN(rel, string(filepath.Separator), 2)[0]] = true
	}
	ignore := map[string]bool{".git": true, rotatree.DirName: true, ".claude": true, ".claude-plugin": true,
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
