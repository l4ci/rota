package mapqa

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	gittest.Write(t, root, rel, content)
}

func TestQuery(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".rota/map/alpha.md", "---\nsubsystem: alpha\n---\nAlpha body\n\n\n")
	write(t, root, ".rota/map/beta.md", "Beta body\n")
	write(t, root, ".rota/map/empty.md", "---\nsubsystem: empty\n---\n")
	tests := []struct {
		name        string
		names       []string
		want        string
		wantMissing []string
	}{
		{"one, frontmatter stripped", []string{"alpha"}, "Alpha body\n", nil},
		{"argument order, blank line between", []string{"beta", "alpha"}, "Beta body\n\nAlpha body\n", nil},
		{"missing reported", []string{"alpha", "nope"}, "Alpha body\n", []string{"nope"}},
		{"empty body prints nothing", []string{"empty", "beta"}, "Beta body\n", nil},
		{"path traversal refused", []string{"../alpha", "a/b", `a\b`, "..", ".", ""}, "", []string{"../alpha", "a/b", `a\b`, "..", ".", ""}},
		{"no names", nil, "", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, missing, err := Query(root, "map", tc.names)
			if err != nil || got != tc.want || !reflect.DeepEqual(missing, tc.wantMissing) {
				t.Errorf("Query = (%q, %v, %v), want (%q, %v)", got, missing, err, tc.want, tc.wantMissing)
			}
		})
	}
}

func TestQueryReadsQADir(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".rota/qa/cli.md", "---\nsurface: cli\n---\nRun it\n")
	got, missing, err := Query(root, "qa", []string{"cli"})
	if err != nil || got != "Run it\n" || len(missing) != 0 {
		t.Errorf("Query = (%q, %v, %v)", got, missing, err)
	}
}

func TestMapIndexBlock(t *testing.T) {
	const head = "## Project Map\n\nSubsystems live in `.rota/MAP.md` (detail in `.rota/map/<name>.md`). Pull with `rota map query <name>`.\n\n"
	t.Run("empty", func(t *testing.T) {
		want := head + "- _(no subsystems yet — write `.rota/map/<name>.md` as you discover subsystems)_"
		if got := MapIndexBlock(t.TempDir()); got != want {
			t.Errorf("got %q", got)
		}
	})
	t.Run("sorted by subsystem name, files without one skipped", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, ".rota/map/a-file.md", "---\nsubsystem: zeta\nsummary: Last one\n---\n")
		write(t, root, ".rota/map/b-file.md", "---\nsubsystem: alpha\n---\n")
		write(t, root, ".rota/map/c-file.md", "---\nsummary: no subsystem\n---\n")
		write(t, root, ".rota/map/d-file.md", "no frontmatter\n")
		want := head + "- **alpha** — (no summary)\n- **zeta** — Last one"
		if got := MapIndexBlock(root); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

func TestQAIndexBlock(t *testing.T) {
	const head = "## Project QA\n\nQA strategies live in `.rota/QA.md` (detail in `.rota/qa/<target>.md`). Pull with `rota qa query <target>`. `/rota-qa run` consumes these; the skill never hardcodes runners.\n\n"
	t.Run("empty", func(t *testing.T) {
		want := head + "- _(no QA strategy yet — run `/rota-qa first-run` to scaffold)_"
		if got := QAIndexBlock(t.TempDir()); got != want {
			t.Errorf("got %q", got)
		}
	})
	t.Run("lists files by name with defaults", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, ".rota/qa/web.md", "---\nsurface: browser\nsummary: Click through\n---\n")
		write(t, root, ".rota/qa/api.md", "no frontmatter\n")
		want := head + "- **api** (?) — (no summary)\n- **web** (browser) — Click through"
		if got := QAIndexBlock(root); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

func TestWriteIndexUpsertsBlock(t *testing.T) {
	root := t.TempDir()
	write(t, root, "AGENTS.md", "# Title\n")
	if _, err := WriteIndex(root, "rota-map", "first body\n\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteIndex(root, "rota-map", "second body"); err != nil {
		t.Fatal(err)
	}
	var found string
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		if raw, err := os.ReadFile(filepath.Join(root, name)); err == nil && strings.Contains(string(raw), "second body") {
			found = string(raw)
		}
	}
	if found == "" || strings.Contains(found, "first body") || strings.Count(found, "second body") != 1 {
		t.Errorf("instructions file after two writes = %q, want one block with the second body", found)
	}
}

func TestStats(t *testing.T) {
	root := t.TempDir()
	write(t, root, "src/a.go", "1\n2\n3\n")
	write(t, root, "src/nonl.go", "1\n2")
	write(t, root, ".rota/map/one.md", "---\nsubsystem: one\ntouched: 2026-01-02\n---\n## Entry points\n\n"+
		"- src/a.go:3 ok\n- src/a.go:4 past the end\n- src/nonl.go:2 last line without newline\n- src/gone.go:1 missing file\n- src/a.go:0 zero\n\n## Other\n- src/a.go:99 not counted\n")
	write(t, root, ".rota/map/two.md", "---\nsubsystem: two\ntouched: 2026-02-03\n---\nno entry points\n")
	write(t, root, ".rota/map/skip.md", "---\nsummary: no subsystem\n---\n")

	got := Stats(root)
	if len(got) != 2 {
		t.Fatalf("Stats = %+v, want 2 rows", got)
	}
	one, two := got[0], got[1]
	if one.Name != "one" || one.Touched != "2026-01-02" || one.EntryPoints != 5 || one.BrokenRefs != 3 || one.Bytes <= 0 {
		t.Errorf("one = %+v", one)
	}
	if two.Name != "two" || two.EntryPoints != 0 || two.BrokenRefs != 0 {
		t.Errorf("two = %+v", two)
	}
}

func TestStatsEmpty(t *testing.T) {
	got := Stats(t.TempDir())
	if got == nil || len(got) != 0 {
		t.Errorf("Stats = %#v, want empty non-nil", got)
	}
}

func TestStatsTouchedFallsBackToGitDate(t *testing.T) {
	root := gittest.NewRepo(t, "main")
	gittest.Commit(t, root, "add map", ".rota/map/g.md", "---\nsubsystem: g\n---\nbody\n")
	want := gittest.Run(t, root, "log", "-1", "--format=%cs", "--", ".rota/map/g.md")
	got := Stats(root)
	if len(got) != 1 || got[0].Touched != want || want == "" {
		t.Errorf("Stats = %+v, want touched %q (git's commit date)", got, want)
	}
}

func TestSoftCap(t *testing.T) {
	tests := []struct {
		name string
		cfg  string
		want int
	}{
		{"no config", "", DefaultCap},
		{"set", `{"map": {"softcap_subsystems": 7}}`, 7},
		{"not a number", `{"map": {"softcap_subsystems": "many"}}`, DefaultCap},
		{"other keys only", `{"map": {}}`, DefaultCap},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.cfg != "" {
				write(t, root, ".rota/config.json", tc.cfg)
			}
			if got := SoftCap(root); got != tc.want {
				t.Errorf("SoftCap = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestCapNote(t *testing.T) {
	want := "project map has 21 subsystems (cap 20); consider merging or retiring stale .rota/map/<name>.md entries"
	if got := CapNote(21, 20); got != want {
		t.Errorf("CapNote = %q", got)
	}
}
