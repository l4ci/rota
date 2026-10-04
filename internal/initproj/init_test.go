package initproj

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/pytest"
)

// fixture is a starting tree: path (relative to the root) to content.
type fixture map[string]string

var fixtures = map[string]fixture{
	"empty": {},
	"vision-heading": {
		".rota/MILESTONES.md": "# Vision\n\nbody line\n# Vision\n",
	},
	"gitignore-no-newline": {
		".gitignore": "node_modules/\n.rota/status.json",
	},
	"gitignore-complete": {
		".gitignore": strings.Join(ignoreLines, "\n") + "\n",
	},
	"gitignore-worktrees-spellings": {
		".gitignore": "/.worktrees\n",
	},
	"gitignore-worktrees-crlf": {
		".gitignore": "dist/\r\n.worktrees/\r\n",
	},
	"initialized": {
		".rota/BACKLOG.md":    "# Backlog\n\n## Bugs\n\n## Features\n\n## Tasks\n\n## Completed\n",
		".rota/KNOWLEDGE.md":  "# Knowledge\n\nmine\n",
		".rota/DECISIONS.md":  "# Decisions\n",
		".rota/MAP.md":        "# Project map\n\nmine\n",
		".rota/MILESTONES.md": "# Milestones\n",
		".rota/counters.json": `{"bugs":1,"features":2,"tasks":3,"milestones":4,"since_refactor":{"features":5,"bugs":6}}` + "\n",
		".rota/status.json":   `{"active":[]}` + "\n",
		".rota/repos.json":    `{"repos":[]}` + "\n",
		".rota/config.json":   `{"work":{"isolation":"worktree"}}` + "\n",
	},
}

func names() []string {
	var n []string
	for k := range fixtures {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}

func writeFixture(t *testing.T, dir string, f fixture) {
	t.Helper()
	for p, c := range f {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// readTree is every path under dir (directories as "/"), minus what the
// comparison ignores.
func readTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			out[rel] = "/"
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[rel] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestInitMatchesBootstrapGolden checks that Init leaves the tree the old
// hv-bootstrap left on every fixture, as recorded in testdata/golden. The
// deliberate differences are the ones in the A9 rulings: no `.rota/bin` directory, the G4 MAP.md text and the G7 config.json key order, plus B2's
// `.rota/verdicts.json` line in the .gitignore block (#55) and no `.rota/bin/`
// line in it (#236), both edited into the golden by hand.
func TestInitMatchesBootstrapGolden(t *testing.T) {
	var want map[string]map[string]string
	pytest.Golden(t, fixtures, &want)
	for _, name := range names() {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeFixture(t, dir, fixtures[name])
			if _, err := Init(dir); err != nil {
				t.Fatal(err)
			}
			got, exp := readTree(t, dir), want[name]
			delete(exp, ".rota/bin")
			for _, tree := range []map[string]string{got, exp} {
				if _, mine := fixtures[name][".rota/MAP.md"]; !mine {
					delete(tree, ".rota/MAP.md")
				}
				if _, mine := fixtures[name][".rota/config.json"]; !mine {
					delete(tree, ".rota/config.json")
				}
			}
			if !reflect.DeepEqual(got, exp) {
				for p := range exp {
					if got[p] != exp[p] {
						t.Errorf("%s differs\n got: %q\nwant: %q", p, got[p], exp[p])
					}
				}
				for p := range got {
					if _, ok := exp[p]; !ok {
						t.Errorf("extra path %s", p)
					}
				}
			}
		})
	}
}

func TestInitIsIdempotent(t *testing.T) {
	for _, name := range names() {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeFixture(t, dir, fixtures[name])
			if _, err := Init(dir); err != nil {
				t.Fatal(err)
			}
			first := readTree(t, dir)
			res, err := Init(dir)
			if err != nil {
				t.Fatal(err)
			}
			if res.Changed() {
				t.Errorf("second run: %+v", res)
			}
			if !reflect.DeepEqual(first, readTree(t, dir)) {
				t.Error("second run changed the tree")
			}
		})
	}
}

func TestInitCreatedListsTheDiff(t *testing.T) {
	dir := t.TempDir()
	res, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{".gitignore", ".rota", ".rota/BACKLOG.md", ".rota/bugs", ".rota/map", ".rota/config.json", ".rota/status.json"} {
		if !contains(res.Created, p) {
			t.Errorf("created lacks %s: %v", p, res.Created)
		}
	}
	if contains(res.Created, ".rota/bin") {
		t.Error("init created .rota/bin")
	}
	if !sort.StringsAreSorted(res.Created) || !res.Changed() {
		t.Errorf("created %v changed %v", res.Created, res.Changed())
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// The seed's keys appear in the order config.Keys lists them (G7).
func TestSeedConfigKeepsSchemaOrder(t *testing.T) {
	schema := map[string]int{}
	for i, k := range config.Keys {
		schema[k.Name] = i
	}
	seed := []string{"issues.providers.github", "issues.providers.gitlab", "issues.label", "issues.autoCreateLabel", "issues.filterMineOnly"}
	prevSchema, prevText := -1, -1
	for _, name := range seed {
		i, ok := schema[name]
		at := strings.Index(configSeed, `"`+name[strings.LastIndex(name, ".")+1:]+`"`)
		if !ok || at < 0 {
			t.Fatalf("%s: in schema %v, in seed at %d", name, ok, at)
		}
		if i <= prevSchema || at <= prevText {
			t.Errorf("%s breaks schema order", name)
		}
		prevSchema, prevText = i, at
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(configSeed), &v); err != nil {
		t.Fatal(err)
	}
}

func TestInitRefusesACorruptCounters(t *testing.T) {
	for _, body := range []string{"{not json", "[]", ""} {
		dir := t.TempDir()
		writeFixture(t, dir, fixture{".rota/counters.json": body})
		_, err := Init(dir)
		if err == nil || !strings.Contains(err.Error(), "counters.json") {
			t.Errorf("%q: %v", body, err)
		}
		if got := readTree(t, dir)[".rota/counters.json"]; got != body {
			t.Errorf("%q: counters rewritten to %q", body, got)
		}
		// nothing else was written: exit 70 leaves the tree as it was
		if tree := readTree(t, dir); len(tree) != 2 {
			t.Errorf("%q: refused init left %v", body, tree)
		}
	}
}

func TestMergeGitignore(t *testing.T) {
	block := strings.Join(ignoreLines, "\n") + "\n"
	cases := []struct {
		name, in string
		exists   bool
		want     string
	}{
		{"new", "", false, block + worktreesBlock},
		{"empty file", "", true, "\n" + block + worktreesBlock},
		{"complete", block + ".worktrees/\n", true, block + ".worktrees/\n"},
		{"slash spelling", block + "/.worktrees\n", true, block + "/.worktrees\n"},
		{"crlf worktrees", block + ".worktrees/\r\n", true, block + ".worktrees/\r\n"},
		{"no trailing newline", "a", true, "a\n" + block + worktreesBlock},
	}
	for _, c := range cases {
		got := MergeGitignore(c.in, c.exists)
		if got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

// Each migration step alone makes the run changed, though nothing is created.
func TestInitChangedWhenOnlyAMigrationWrote(t *testing.T) {
	seeded := func(t *testing.T) string {
		dir := t.TempDir()
		if _, err := Init(dir); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	for name, mutate := range map[string]func(dir string){
		"gitignore block": func(d string) { os.WriteFile(filepath.Join(d, ".gitignore"), []byte("x\n"), 0o644) },
		"milestones heading": func(d string) {
			os.WriteFile(filepath.Join(d, ".rota", "MILESTONES.md"), []byte("# Vision\n"), 0o644)
		},
	} {
		dir := seeded(t)
		mutate(dir)
		res, err := Init(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Created) != 0 || !res.Changed() {
			t.Errorf("%s: created %v changed %v", name, res.Created, res.Changed())
		}
		if again, _ := Init(dir); again.Changed() {
			t.Errorf("%s: second run changed", name)
		}
	}
}
