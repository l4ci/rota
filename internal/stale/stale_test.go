package stale

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func date(t *testing.T, s string) time.Time {
	t.Helper()
	d, ok := ParseDate(s)
	if !ok {
		t.Fatalf("bad date %q", s)
	}
	return d
}

func TestParseDate(t *testing.T) {
	for in, ok := range map[string]bool{
		"2026-10-02": true, " 2026-10-02 ": true, "20261002": true,
		"2026-1-2": false, "2026-13-01": false, "2026-02-30": false, "": false, "yesterday": false,
		"2026-10-02T10:00:00": false, "٢٠٢٦-١٠-٠٢": false, "0000-01-01": false,
	} {
		if _, got := ParseDate(in); got != ok {
			t.Errorf("ParseDate(%q) ok = %v, want %v", in, got, ok)
		}
	}
}

func TestDaysBetween(t *testing.T) {
	a, b := date(t, "2026-10-02"), date(t, "2026-09-02")
	if daysBetween(a, b) != 30 || daysBetween(b, a) != -30 || daysBetween(a, a) != 0 {
		t.Errorf("daysBetween: %d %d", daysBetween(a, b), daysBetween(b, a))
	}
	if !isStale(b, a, 30) || isStale(b, a, 31) {
		t.Error("isStale boundary")
	}
}

// project is a git repo with the given .rota files committed.
func project(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.name", "T")
	run("config", "user.email", "t@example.com")
	run("config", "commit.gpgsign", "false")
	os.WriteFile(filepath.Join(root, "x"), []byte("x"), 0o644)
	for p, c := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte(c), 0o644)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "init")
	return root
}

func names(es []Entry) []string {
	out := []string{}
	for _, e := range es {
		out = append(out, e.Name+" "+e.Date)
	}
	return out
}

func TestFindTodo(t *testing.T) {
	root := project(t, map[string]string{".rota/BACKLOG.md": "## Bugs\n" +
		"- **[B01] [P1] old.** x Captured: 2026-01-01\n" +
		"- **[B02] [P1] fresh.** y Captured: 2026-09-15\n" +
		"- **[B03] [P1] undated.** z\n" +
		"- **[B04] [P1] junk.** w Captured: not-a-date\n" +
		"- **[B05] [P1] compact.** v Captured: 20260101\n\n## Completed\n- ~~**[B09] [P1] d.** q Captured: 2020-01-01~~ Done 2026-09-30 [`a`]\n"})
	got, err := Find(root, "todo", 90, date(t, "2026-10-02"))
	if err != nil || !reflect.DeepEqual(names(got), []string{"B01 2026-01-01", "B05 2026-01-01"}) {
		t.Errorf("todo = %v %v", names(got), err)
	}
	got, _ = Find(root, "todo", 0, date(t, "2026-10-02"))
	if len(got) != 3 {
		t.Errorf("--days 0: %v", names(got))
	}
	if got, _ := Find(t.TempDir(), "todo", 0, date(t, "2026-10-02")); got != nil {
		t.Errorf("no backlog: %v", got)
	}
}

func TestFindKnowledgeUsesTheFileCommitDate(t *testing.T) {
	root := project(t, map[string]string{".rota/KNOWLEDGE.md": "# K\n\n## Alpha\nx\n\n## Beta, gamma\ny\n\n## Delta  \nz\n"})
	far := date(t, "2999-01-01")
	got, err := Find(root, "knowledge", 90, far)
	if err != nil || len(got) != 3 || got[0].Name != "Alpha" || got[1].Name != "Beta, gamma" || got[2].Name != "Delta" {
		t.Fatalf("knowledge = %v %v", names(got), err)
	}
	if _, ok := ParseDate(got[0].Date); !ok {
		t.Errorf("date = %q", got[0].Date)
	}
	if got, _ := Find(root, "knowledge", 90, time.Now()); got != nil {
		t.Errorf("a file committed today is stale: %v", names(got))
	}
	if got, _ := Find(t.TempDir(), "knowledge", 0, far); got != nil {
		t.Errorf("missing file: %v", got)
	}
}

func TestFindMap(t *testing.T) {
	fm := func(sub, touched string) string {
		s := "---\nsubsystem: " + sub + "\n"
		if touched != "" {
			s += "touched: " + touched + "\n"
		}
		return s + "---\nbody\n"
	}
	root := project(t, map[string]string{
		".rota/map/auth.md": fm("auth", "2026-01-01"), ".rota/map/cache.md": fm("cache", "2026-09-30"),
		".rota/map/billing.md": fm("billing", ""), ".rota/map/nosub.md": "---\ntitle: x\n---\n", ".rota/map/plain.md": "no frontmatter\n",
		".rota/map/zeta.md": fm("aaa", "2025-12-31"), ".rota/map/bad.md": fm("baddate", "soon"),
	})
	got, err := Find(root, "map", 90, date(t, "2026-10-02"))
	if err != nil {
		t.Fatal(err)
	}
	// Sorted by subsystem; billing and baddate have no touched date, so their git date (today) decides: fresh.
	if !reflect.DeepEqual(names(got), []string{"aaa 2025-12-31", "auth 2026-01-01"}) {
		t.Errorf("map = %v", names(got))
	}
	got, _ = Find(root, "map", 90, date(t, "2999-01-01"))
	if len(got) != 5 || got[0].Name != "aaa" || got[1].Name != "auth" || got[2].Name != "baddate" || got[3].Name != "billing" || got[4].Name != "cache" {
		t.Errorf("map far future = %v", names(got))
	}
	if got, _ := Find(t.TempDir(), "map", 0, date(t, "2999-01-01")); got != nil {
		t.Errorf("missing dir: %v", got)
	}
}

func TestFindUntrackedMapFileIsNeverStale(t *testing.T) {
	root := project(t, nil)
	os.MkdirAll(filepath.Join(root, ".rota", "map"), 0o755)
	os.WriteFile(filepath.Join(root, ".rota", "map", "a.md"), []byte("---\nsubsystem: a\n---\n"), 0o644)
	if got, _ := Find(root, "map", 0, date(t, "2999-01-01")); got != nil {
		t.Errorf("untracked file listed: %v", names(got))
	}
}

func TestFindUnknownKind(t *testing.T) {
	if _, err := Find(t.TempDir(), "plans", 1, time.Now()); !errors.Is(err, ErrKind) || !strings.Contains(err.Error(), "plans") {
		t.Errorf("err = %v", err)
	}
}
