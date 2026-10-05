package backlog

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/pystr"
)

// Imported is one upstream issue a backlog item points at through a
// `GH: #N` or `GL: #N` cross-reference (an entry of hv-issues-imported).
type Imported struct {
	Provider string // github | gitlab
	Repo     string // the Repos: entry the item names; "" when it has none
	Issue    int
	ItemID   string // "F69"
	Status   string // open | archived
}

var (
	GHRefRe  = regexp.MustCompile(`GH:[` + pystr.SpaceClass + `]*#(\p{Nd}+)`)
	GLRefRe  = regexp.MustCompile(`GL:[` + pystr.SpaceClass + `]*#(\p{Nd}+)`)
	reposRe  = regexp.MustCompile(`Repos:[` + pystr.SpaceClass + `]*([^\n]+?)(?:[` + pystr.SpaceClass + `]+(?:Detail|Related|Milestone):|$)`)
	bulletID = regexp.MustCompile(`\[([` + ItemLetters + `]\p{Nd}+)\]`)
)

type importedKey struct {
	provider string
	issue    int
	item     string
	repo     string
}

// ScanImported indexes the GH/GL cross-references of BACKLOG.md (open
// sections; ## Completed is skipped), ARCHIVE.md (every section, archived) and
// the per-item detail files .rota/{bugs,features,tasks}/*.md (item = file stem,
// every line). A multi-repo `Repos:` list gives one entry per repo; an
// open entry replaces an archived one for the same key. The result keeps the
// first-seen order, and detail files are read in name order (the old helper
// used directory order). forRepo, when not empty, keeps the entries of that
// Repos: name. Missing files are skipped.
func ScanImported(root, forRepo string) []Imported {
	rota := filepath.Join(root, ".rota")
	seen := map[importedKey]int{}
	var out []Imported
	register := func(provider string, issue int, item string, repos []string, status string) {
		for _, repo := range repos {
			k := importedKey{provider, issue, item, repo}
			e := Imported{Provider: provider, Repo: repo, Issue: issue, ItemID: item, Status: status}
			if i, ok := seen[k]; ok {
				if status == "open" && out[i].Status == "archived" {
					out[i] = e
				}
				continue
			}
			seen[k] = len(out)
			out = append(out, e)
		}
	}
	scanLine := func(line, item, status string) {
		repos := importedRepos(line)
		for _, m := range GHRefRe.FindAllStringSubmatch(line, -1) {
			n, err := Atoi(m[1])
			if err == nil {
				register("github", n, item, repos, status)
			}
		}
		for _, m := range GLRefRe.FindAllStringSubmatch(line, -1) {
			n, err := Atoi(m[1])
			if err == nil {
				register("gitlab", n, item, repos, status)
			}
		}
	}
	scanFile := func(name, status string, stopAtCompleted bool) {
		text, err := fsio.ReadText(filepath.Join(rota, name))
		if err != nil {
			return
		}
		in := false
		for _, line := range pystr.Splitlines(text) {
			if strings.HasPrefix(line, "## ") {
				if stopAtCompleted && strings.ToLower(pystr.Strip(line[3:])) == "completed" {
					in = false
					continue
				}
				in = true
				continue
			}
			if !in {
				continue
			}
			if m := bulletID.FindStringSubmatch(line); m != nil {
				scanLine(line, m[1], status)
			}
		}
	}
	scanFile("BACKLOG.md", "open", true)
	scanFile("ARCHIVE.md", "archived", false)
	for _, sub := range []string{"bugs", "features", "tasks"} {
		entries, err := os.ReadDir(filepath.Join(rota, sub))
		if err != nil {
			continue
		}
		var names []string
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, name := range names {
			text, err := fsio.ReadText(filepath.Join(rota, sub, name))
			if err != nil {
				continue
			}
			item := strings.TrimSuffix(name, ".md")
			for _, line := range pystr.Splitlines(text) {
				scanLine(line, item, "open")
			}
		}
	}
	if forRepo == "" {
		return out
	}
	var kept []Imported
	for _, e := range out {
		if e.Repo == forRepo {
			kept = append(kept, e)
		}
	}
	return kept
}

// importedRepos is the names of a line's Repos: field, or [""] when it has none.
func importedRepos(line string) []string {
	m := reposRe.FindStringSubmatch(line)
	if m == nil {
		return []string{""}
	}
	var parts []string
	for _, p := range strings.Split(pystr.Strip(m[1]), ",") {
		if p = pystr.Strip(p); p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return []string{""}
	}
	return parts
}
