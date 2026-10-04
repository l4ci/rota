package backlog

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func day(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func readFile(t *testing.T, f *File, name string) string {
	t.Helper()
	b, err := os.ReadFile(f.rota(name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func writeHV(t *testing.T, f *File, name, content string) {
	t.Helper()
	if err := os.WriteFile(f.rota(name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const completed = "# TODO\n\n## Bugs\n- **[B01] [P1] a.** x\n\n## Completed\n" +
	"- ~~**[B02] [P1] old.** x~~ Done 2026-09-01 [`a1`]\n" +
	"- ~~**[B03] [P1] edge.** x~~ Done 2026-09-27 [`a2`]\n" +
	"- ~~**[B04] [P1] kept.** x~~ Done 2026-09-28 [`a3`] (dropped: gone)\n" +
	"note\n"

func TestArchiveMovesOldDoneLines(t *testing.T) {
	f := fileBackend(t, completed)
	moved, err := f.Archive(5, day(t, "2026-10-02")) // cutoff 2026-09-27: strictly older moves
	if err != nil || moved != 1 {
		t.Fatalf("Archive = %d, %v", moved, err)
	}
	// The section loses its trailing newline, as the helper leaves it.
	want := "# TODO\n\n## Bugs\n- **[B01] [P1] a.** x\n\n## Completed\n" +
		"- ~~**[B03] [P1] edge.** x~~ Done 2026-09-27 [`a2`]\n" +
		"- ~~**[B04] [P1] kept.** x~~ Done 2026-09-28 [`a3`] (dropped: gone)\nnote"
	if got := readFile(t, f, "BACKLOG.md"); got != want {
		t.Errorf("BACKLOG.md:\n%q\nwant\n%q", got, want)
	}
	// The header's blank line goes: the helper right-trims the existing text, then adds "\n".
	wantArchive := "# Archive\n\nCompleted items older than the active window.\n- ~~**[B02] [P1] old.** x~~ Done 2026-09-01 [`a1`]\n"
	if got := readFile(t, f, "ARCHIVE.md"); got != wantArchive {
		t.Errorf("ARCHIVE.md:\n%q", got)
	}
	// A second run finds nothing and leaves both files alone.
	before := readFile(t, f, "BACKLOG.md")
	if moved, _ := f.Archive(5, day(t, "2026-10-02")); moved != 0 || readFile(t, f, "BACKLOG.md") != before {
		t.Error("second run changed something")
	}
}

func TestArchiveAppendsToExistingArchive(t *testing.T) {
	f := fileBackend(t, completed)
	writeHV(t, f, "ARCHIVE.md", "# Archive\n\n- ~~**[B00] x.** y~~ Done 2026-01-01 [`z`]  \n\n\n")
	if moved, err := f.Archive(0, day(t, "2026-10-02")); err != nil || moved != 3 {
		t.Fatalf("Archive = %d, %v", moved, err)
	}
	want := "# Archive\n\n- ~~**[B00] x.** y~~ Done 2026-01-01 [`z`]\n" +
		"- ~~**[B02] [P1] old.** x~~ Done 2026-09-01 [`a1`]\n" +
		"- ~~**[B03] [P1] edge.** x~~ Done 2026-09-27 [`a2`]\n" +
		"- ~~**[B04] [P1] kept.** x~~ Done 2026-09-28 [`a3`] (dropped: gone)\n"
	if got := readFile(t, f, "ARCHIVE.md"); got != want {
		t.Errorf("ARCHIVE.md:\n%q\nwant\n%q", got, want)
	}
}

func TestArchiveNoWork(t *testing.T) {
	for name, md := range map[string]string{"no section": "# TODO\n\n## Bugs\n- **[B01] [P1] a.** x\n", "empty": "# TODO\n\n## Completed\n", "": ""} {
		f := fileBackend(t, md)
		if moved, err := f.Archive(0, day(t, "2026-10-02")); err != nil || moved != 0 {
			t.Errorf("%q: %d, %v", name, moved, err)
		}
		if _, err := os.Stat(f.rota("ARCHIVE.md")); err == nil {
			t.Errorf("%q: ARCHIVE.md created", name)
		}
	}
}

func TestArchiveSectionFollowedByAnother(t *testing.T) {
	f := fileBackend(t, "## Completed\n- ~~**[B02] x.** y~~ Done 2026-01-01 [`a`]\n\n## Notes\nhi\n")
	if moved, _ := f.Archive(5, day(t, "2026-10-02")); moved != 1 {
		t.Fatal(moved)
	}
	if got := readFile(t, f, "BACKLOG.md"); got != "## Completed\n## Notes\nhi\n" {
		t.Errorf("BACKLOG.md = %q", got)
	}
}

func TestArchiveInvalidDateWritesNothing(t *testing.T) {
	md := "## Completed\n- ~~**[B02] x.** y~~ Done 2026-13-45 [`a`]\n- ~~**[B03] x.** y~~ Done 2000-01-01 [`a`]\n"
	f := fileBackend(t, md)
	if _, err := f.Archive(5, day(t, "2026-10-02")); !errors.Is(err, ErrBadDate) {
		t.Fatalf("err = %v", err)
	}
	if readFile(t, f, "BACKLOG.md") != md {
		t.Error("BACKLOG.md changed")
	}
	if _, err := os.Stat(f.rota("ARCHIVE.md")); err == nil {
		t.Error("ARCHIVE.md created")
	}
}

func TestBackfillSince(t *testing.T) {
	md := "# TODO\n\n## Tasks\n- **[T01] t.** a\n\n## Bugs\n- **[B01] [P1] b.** x Since: old\n- **[B02] [P1] c.** y   \n  - **[B03] [P1] nested.** z\n- **[B04] [P1] d.** w Since:\n\n## Completed\n- ~~**[B09] [P1] done.** q~~ Done 2026-09-30 [`abc`]\n"
	f := fileBackend(t, md)
	n, err := f.BackfillSince("abc1234")
	if err != nil || n != 3 {
		t.Fatalf("BackfillSince = %d, %v", n, err)
	}
	want := "# TODO\n\n## Tasks\n- **[T01] t.** a Since: abc1234\n\n## Bugs\n- **[B01] [P1] b.** x Since: old\n- **[B02] [P1] c.** y Since: abc1234\n  - **[B03] [P1] nested.** z\n- **[B04] [P1] d.** w Since: Since: abc1234\n\n## Completed\n- ~~**[B09] [P1] done.** q~~ Done 2026-09-30 [`abc`]\n"
	if got := readFile(t, f, "BACKLOG.md"); got != want {
		t.Errorf("BACKLOG.md:\n%s\nwant\n%s", got, want)
	}
	if n, _ := f.BackfillSince("abc1234"); n != 0 {
		t.Errorf("second run stamped %d", n)
	}
	if n, err := fileBackend(t, "").BackfillSince("abc"); n != 0 || err != nil {
		t.Errorf("no BACKLOG.md: %d, %v", n, err)
	}
}

func TestRefactorCounters(t *testing.T) {
	f := fileBackend(t, "")
	feats, bugs, err := f.RefactorAge()
	if err != nil || feats != json.Number("0") || bugs != json.Number("0") {
		t.Fatalf("age without counters.json = %v %v %v", feats, bugs, err)
	}
	writeHV(t, f, "counters.json", `{"bugs": 3, "since_refactor": {"extra": 1, "features": 2, "bugs": 5}, "tasks": 1}`)
	feats, bugs, _ = f.RefactorAge()
	if feats != json.Number("2") || bugs != json.Number("5") {
		t.Errorf("age = %v %v", feats, bugs)
	}
	changed, err := f.RefactorReset()
	if err != nil || !changed {
		t.Fatalf("reset = %v %v", changed, err)
	}
	want := "{\n  \"bugs\": 3,\n  \"since_refactor\": {\n    \"features\": 0,\n    \"bugs\": 0\n  },\n  \"tasks\": 1\n}\n"
	if got := readFile(t, f, "counters.json"); got != want {
		t.Errorf("counters.json:\n%s", got)
	}
	if changed, _ := f.RefactorReset(); changed {
		t.Error("reset of zeros reports a change")
	}
	// A missing file is created.
	g := fileBackend(t, "")
	if changed, err := g.RefactorReset(); changed || err != nil {
		t.Fatalf("reset without file = %v %v", changed, err)
	}
	if got := readFile(t, g, "counters.json"); got != "{\n  \"since_refactor\": {\n    \"features\": 0,\n    \"bugs\": 0\n  }\n}\n" {
		t.Errorf("created counters.json:\n%s", got)
	}
}

func TestRefactorAgeBadShapes(t *testing.T) {
	f := fileBackend(t, "")
	writeHV(t, f, "counters.json", `{"since_refactor": 5}`)
	if _, _, err := f.RefactorAge(); !errors.Is(err, ErrCounters) {
		t.Errorf("age with a scalar since_refactor: %v", err)
	}
	if changed, err := f.RefactorReset(); err != nil || !changed {
		t.Errorf("reset replaces a scalar since_refactor: %v %v", changed, err)
	}
	writeHV(t, f, "counters.json", `[1]`)
	if _, _, err := f.RefactorAge(); err == nil {
		t.Error("age accepted a list")
	}
}

// ---- drift ---------------------------------------------------------------------

func gitRepo(t *testing.T, dir string) {
	t.Helper()
	for _, a := range [][]string{{"init", "-q", "-b", "main"}, {"config", "user.name", "T"}, {"config", "user.email", "t@example.com"}, {"config", "commit.gpgsign", "false"}} {
		gitIn(t, dir, a...)
	}
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func commit(t *testing.T, dir, subject, file, content string) string {
	t.Helper()
	p := filepath.Join(dir, file)
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte(content), 0o644)
	gitIn(t, dir, "add", file)
	gitIn(t, dir, "commit", "-q", "-m", subject)
	return gitIn(t, dir, "rev-parse", "--short", "HEAD")
}

func TestDrift(t *testing.T) {
	f := fileBackend(t, "")
	gitRepo(t, f.Root)
	h1 := commit(t, f.Root, "chore: init", "a.txt", "x")
	commit(t, f.Root, "fix: [B01] early", "b.txt", "x")
	h3 := commit(t, f.Root, "feat: [B02] [F01] done", "c.txt", "x")
	commit(t, f.Root, "feat: [B01] and [B02] again [X9] [B05]", "src/lex.go", "func ParserCore() {}\n")
	md := "# TODO\n\n## Bugs\n" +
		"- **[B01] [P1] Early.** x Since: " + h3 + "\n" + // the fix predates the anchor
		"- **[B02] [P1] Both.** y Since: " + h1 + "\n" +
		"- **[B03] [P1] Unknown anchor `ParserCore`.** z Since: deadbeef\n" +
		"- **[B04] [P1] Dash anchor.** z Since: --exec=x\n" +
		"- **[B06] [P1] Add `ParserCore`.** z Since: " + h1 + "\n" +
		"\n## Features\n- **[F01] [Major] No anchor.** w\n\n## Completed\n- ~~**[B05] [P1] closed.** q~~ Done 2026-09-30 [`abc`]\n"
	writeHV(t, f, "BACKLOG.md", md)
	drift, syms, err := f.Drift([]Target{{Dir: f.Root}})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range drift {
		var hs []string
		for _, c := range d.Commits {
			hs = append(hs, c.Subject)
		}
		got = append(got, d.ID+d.Type+": "+strings.Join(hs, " | "))
	}
	want := []string{
		"B01B: feat: [B01] and [B02] again [X9] [B05]",
		"B02B: feat: [B02] [F01] done | feat: [B01] and [B02] again [X9] [B05]", // oldest first
		"F01F: feat: [B02] [F01] done",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("drift:\n%q\nwant\n%q", got, want)
	}
	if len(drift[0].Commits[0].Hash) != 7 || drift[0].Commits[0].Repo != "" {
		t.Errorf("commit = %+v", drift[0].Commits[0])
	}
	if len(syms) != 1 || syms[0].ID != "B06" || !reflect.DeepEqual(syms[0].Symbols, []string{"ParserCore"}) || !reflect.DeepEqual(syms[0].Files, []string{"src/lex.go"}) {
		t.Errorf("symbol drift = %+v", syms)
	}
}

func TestDriftEmptyCases(t *testing.T) {
	f := fileBackend(t, "")
	if d, s, err := f.Drift([]Target{{Dir: f.Root}}); d != nil || s != nil || err != nil {
		t.Errorf("no BACKLOG.md: %v %v %v", d, s, err)
	}
	writeHV(t, f, "BACKLOG.md", "# TODO\n\n## Completed\n- ~~**[B01] a.** x~~ Done 2026-09-30 [`abc`]\n")
	if d, s, err := f.Drift([]Target{{Dir: f.Root}}); d != nil || s != nil || err != nil {
		t.Errorf("no open items: %v %v %v", d, s, err)
	}
	// Not a git repo: nothing to walk, no error.
	writeHV(t, f, "BACKLOG.md", "## Bugs\n- **[B01] [P1] a.** x\n")
	if d, _, err := f.Drift([]Target{{Dir: f.Root}}); len(d) != 0 || err != nil {
		t.Errorf("no git: %v %v", d, err)
	}
}

func TestDriftUmbrellaTargets(t *testing.T) {
	f := fileBackend(t, "## Bugs\n- **[B01] [P1] a.** x\n")
	web, api := filepath.Join(f.Root, "web"), filepath.Join(f.Root, "api")
	for _, d := range []string{web, api} {
		os.MkdirAll(d, 0o755)
		gitRepo(t, d)
	}
	commit(t, web, "fix: [B01] web", "f.txt", "x")
	commit(t, api, "fix: [B01] api", "f.txt", "x")
	drift, _, err := f.Drift([]Target{{Name: "web", Dir: web}, {Name: "api", Dir: api}})
	if err != nil || len(drift) != 1 || len(drift[0].Commits) != 2 {
		t.Fatalf("drift = %+v %v", drift, err)
	}
	// Reversed as a whole: the last target's commits come first.
	if drift[0].Commits[0].Repo != "api" || drift[0].Commits[1].Repo != "web" {
		t.Errorf("commits = %+v", drift[0].Commits)
	}
}

// The expected values come from extract_symbols in the retired drift helper.
func TestExtractSymbols(t *testing.T) {
	for _, c := range []struct {
		bullet, id string
		want       []string
	}{
		{"- **[B01] [P1] Add `ParserCore` and snake_case_thing.** Detail: `.rota/bugs/B01.md` Since: abc", "B01", []string{"ParserCore", "snake_case_thing"}},
		{"- **[B02] [P1] Needs rota-new-helper and config/loader.yaml plus lib.py.** y Since: abc", "B02", []string{"rota-new-helper", "config/loader.yaml", "loader.yaml", "lib.py"}},
		{"- **[B05] [P2] Rework src/größe/modul.go and naïve_parser here.** v Since: abc", "B05", []string{"src/größe/modul.go", "modul.go"}},
		{"- **[B06] [P2] Short: `abc` and `ab_cd`.** u", "B06", []string{"ab_cd"}},
		{"- **[B07] [P2] CamelCaseThing and GrößeWert and fooBarBaz and HTTPServer and a.b.c.d.** t", "B07", []string{"CamelCaseThing"}},
		{"- **[B08] [P2] xé_snake_case and snake_écase and snake_caseé and plain_snake_two.** t", "B08", []string{"plain_snake_two"}},
		{"- **[B09] [P2] path/éx/y.go and é/a.b and fileé.py and dir/file.tool.** t Related: [B01]", "B09", []string{"path/éx/y.go", "é/a.b", "dir/file.tool", "y.go", "fileé.py", "file.tool"}},
		{"- **[B10] [P2] [B10] and [x] ref `[B10]` `**bold**` x.** t", "B10", []string{"**bold**"}},
	} {
		if got := ExtractSymbols(c.bullet, c.id); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got %q\nwant %q", c.id, got, c.want)
		}
	}
	var many []string
	for i := 1; i <= 12; i++ {
		many = append(many, "`sym_"+string(rune('a'+i))+"_x`")
	}
	if got := ExtractSymbols("- **[B01] [P1] "+strings.Join(many, " ")+".** x", "B01"); len(got) != 8 {
		t.Errorf("cap: %d symbols", len(got))
	}
}
