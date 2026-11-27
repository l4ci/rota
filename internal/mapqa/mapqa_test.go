package mapqa

import (
	"context"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/stamp"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/cfgdir"
	"github.com/l4ci/rota/internal/config"
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

	got := Stats(git.Repo{Dir: root}, root)
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
	got := Stats(git.Repo{}, t.TempDir())
	if got == nil || len(got) != 0 {
		t.Errorf("Stats = %#v, want empty non-nil", got)
	}
}

func TestStatsTouchedFallsBackToGitDate(t *testing.T) {
	root := gittest.NewRepo(t, "main")
	gittest.Commit(t, root, "add map", ".rota/map/g.md", "---\nsubsystem: g\n---\nbody\n")
	want := gittest.Run(t, root, "log", "-1", "--format=%cs", "--", ".rota/map/g.md")
	got := Stats(git.Repo{Dir: root}, root)
	if len(got) != 1 || got[0].Touched != want || want == "" {
		t.Errorf("Stats = %+v, want touched %q (git's commit date)", got, want)
	}
}

// The file name is a map file's one identity: a `subsystem:` field that differs
// or is absent changes nothing, and every name the index prints can be queried.
func TestMapFilesAreNamedByFileName(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".rota/map/auth.md", "---\nsubsystem: authentication\nsummary: Logins\ntouched: 2026-01-02\n---\nauth body\n")
	write(t, root, ".rota/map/auth-ui.md", "---\nsubsystem: aaa\ntouched: 2026-01-02\n---\nui body\n")
	write(t, root, ".rota/map/bare.md", "bare body\n")
	write(t, root, ".rota/map/.md", "no name\n")
	want := []string{"auth", "auth-ui", "bare"}

	var indexed []string
	for _, m := range regexp.MustCompile(`(?m)^- \*\*(.+?)\*\*`).FindAllStringSubmatch(MapIndexBlock(root), -1) {
		indexed = append(indexed, m[1])
	}
	if !reflect.DeepEqual(indexed, want) {
		t.Errorf("index names = %v, want %v", indexed, want)
	}
	if _, missing, err := Query(root, "map", indexed); err != nil || len(missing) != 0 {
		t.Errorf("Query(indexed) missing = %v, err = %v", missing, err)
	}
	var stats []string
	for _, s := range Stats(git.Repo{Dir: root}, root) {
		stats = append(stats, s.Name)
	}
	if !reflect.DeepEqual(stats, want) {
		t.Errorf("stats names = %v, want %v", stats, want)
	}
}

// An unparseable touched value is dated by git, the rule stale.FindReport also uses.
func TestStatsUnparseableTouchedFallsBackToGitDate(t *testing.T) {
	root := gittest.NewRepo(t, "main")
	gittest.Commit(t, root, "add map", ".rota/map/g.md", "---\nsubsystem: g\ntouched: yesterday\n---\nbody\n")
	want := gittest.Run(t, root, "log", "-1", "--format=%cs", "--", ".rota/map/g.md")
	got := Stats(git.Repo{Dir: root}, root)
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
		{"no config", "", 20},
		{"set", `{"map": {"softcap_subsystems": 7}}`, 7},
		{"not a number", `{"map": {"softcap_subsystems": "many"}}`, 20},
		{"other keys only", `{"map": {}}`, 20},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.cfg != "" {
				write(t, root, ".rota/config.json", tc.cfg)
			}
			if got := SoftCap(config.Load(cfgdir.OSEnv(), root)); got != tc.want {
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

func TestStatsCommitsBehind(t *testing.T) {
	root := gittest.NewRepo(t, "main")
	mapped := gittest.Run(t, root, "rev-parse", "HEAD")
	for _, n := range []string{"a", "b", "c"} {
		gittest.Commit(t, root, "churn "+n, n+".txt", n+"\n")
	}
	write(t, root, ".rota/map/fresh.md", "---\nsubsystem: fresh\nlast_mapped_commit: "+gittest.Run(t, root, "rev-parse", "HEAD")+"\n---\nbody\n")
	write(t, root, ".rota/map/old.md", "---\nsubsystem: old\nlast_mapped_commit: "+mapped+"\n---\nbody\n")
	write(t, root, ".rota/map/unmapped.md", "---\nsubsystem: unmapped\n---\nbody\n")
	write(t, root, ".rota/map/gone.md", "---\nsubsystem: gone\nlast_mapped_commit: 0123456789abcdef0123456789abcdef01234567\n---\nbody\n")

	got := map[string]*int{}
	for _, s := range Stats(git.Repo{Dir: root}, root) {
		got[s.Name] = s.CommitsBehind
	}
	if got["fresh"] == nil || *got["fresh"] != 0 {
		t.Errorf("fresh = %v, want 0", got["fresh"])
	}
	if got["old"] == nil || *got["old"] != 3 {
		t.Errorf("old = %v, want 3", got["old"])
	}
	if got["unmapped"] != nil {
		t.Errorf("unmapped = %d, want nil (no last_mapped_commit)", *got["unmapped"])
	}
	if got["gone"] != nil {
		t.Errorf("gone = %d, want nil (sha not in history)", *got["gone"])
	}
}

func TestStaleCommits(t *testing.T) {
	tests := []struct {
		name string
		cfg  string
		want int
	}{
		{"no config", "", 50},
		{"set", `{"map": {"staleCommits": 7}}`, 7},
		{"zero disables", `{"map": {"staleCommits": 0}}`, 0},
		{"negative", `{"map": {"staleCommits": -1}}`, 50},
		{"not a number", `{"map": {"staleCommits": "many"}}`, 50},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.cfg != "" {
				write(t, root, ".rota/config.json", tc.cfg)
			}
			if got := StaleCommits(config.Load(cfgdir.OSEnv(), root)); got != tc.want {
				t.Errorf("StaleCommits = %d, want %d", got, tc.want)
			}
		})
	}
}

// Stats asks the injected repo, never real git: the touch date and the commit
// count come from the runner; a git that cannot run is an error on the row, not
// a rewritten branch, and a sha git does not know stays a nil count.
func TestStatsReadsThroughTheInjectedRepo(t *testing.T) {
	root := t.TempDir()
	sha := "0123456789abcdef0123456789abcdef01234567"
	write(t, root, ".rota/map/old.md", "---\nsubsystem: old\nlast_mapped_commit: "+sha+"\n---\nbody\n")
	write(t, root, ".rota/map/gone.md", "---\nsubsystem: gone\nlast_mapped_commit: ffffffffffffffffffffffffffffffffffffffff\n---\nbody\n")
	runner := func(fail error) git.Repo {
		return git.Repo{Dir: root, Runner: func(_ context.Context, _ string, args ...string) (git.Result, error) {
			switch {
			case fail != nil:
				return git.Result{}, fail
			case args[0] == "log":
				return git.Result{Stdout: "2024-03-05\n"}, nil
			case strings.Contains(strings.Join(args, " "), sha):
				return git.Result{Stdout: "7\n"}, nil
			}
			return git.Result{ExitCode: 128}, nil // unknown commit
		}}
	}
	byName := func(repo git.Repo) map[string]Subsystem {
		m := map[string]Subsystem{}
		for _, s := range Stats(repo, root) {
			m[s.Name] = s
		}
		return m
	}
	got := byName(runner(nil))
	if o := got["old"]; o.Touched != "2024-03-05" || o.CommitsBehind == nil || *o.CommitsBehind != 7 || o.CommitsBehindErr != nil {
		t.Errorf("old = %+v, want touched 2024-03-05, 7 behind", o)
	}
	if g := got["gone"]; g.CommitsBehind != nil || g.CommitsBehindErr != nil {
		t.Errorf("gone = %+v, want nil count and no error", g)
	}
	if o := byName(runner(git.ErrNoGit))["old"]; o.CommitsBehind != nil || o.CommitsBehindErr == nil {
		t.Errorf("old with broken git = %+v, want nil count and an error", o)
	}
}

func fakeGit(stdout string, exit int) git.Repo {
	return git.Repo{Runner: func(_ context.Context, _ string, _ ...string) (git.Result, error) {
		return git.Result{Stdout: stdout, ExitCode: exit}, nil
	}}
}

func TestCommitDateReadsThroughTheSuppliedRunner(t *testing.T) {
	if d, ok := CommitDate(fakeGit("2024-03-05\n", 0), "a.md"); !ok || stamp.Date(d) != "2024-03-05" {
		t.Errorf("CommitDate = %v, %v", d, ok)
	}
	for name, repo := range map[string]git.Repo{
		"git fails":  fakeGit("", 128),
		"no history": fakeGit("\n", 0),
		"bad date":   fakeGit("not-a-date\n", 0),
	} {
		if _, ok := CommitDate(repo, "a.md"); ok {
			t.Errorf("%s: want not ok", name)
		}
	}
}

func TestTouchedInPrefersFrontmatterOverGit(t *testing.T) {
	e := Entry{FM: map[string]any{"touched": "2020-01-02"}, Path: "/r/.rota/map/a.md"}
	if d, ok := e.TouchedIn(fakeGit("2024-03-05\n", 0), "/r"); !ok || stamp.Date(d) != "2020-01-02" {
		t.Errorf("frontmatter lost: %v %v", d, ok)
	}
	e.FM = nil
	if d, ok := e.TouchedIn(fakeGit("2024-03-05\n", 0), "/r"); !ok || stamp.Date(d) != "2024-03-05" {
		t.Errorf("git fallback: %v %v", d, ok)
	}
}
