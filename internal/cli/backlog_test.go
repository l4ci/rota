package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/backlog/trackertest"
	"github.com/l4ci/rota/internal/jsonx"
)

func get(v any, path ...string) any {
	for _, p := range path {
		switch t := v.(type) {
		case map[string]any:
			v = t[p]
		case *jsonx.Object:
			v, _ = t.Get(p)
		default:
			return nil
		}
	}
	return v
}

func dataOf(env map[string]any) *jsonx.Object {
	d, _ := env["data"].(*jsonx.Object)
	return d
}

// Issue mode with no resolvable forge (no origin, no issues.provider) is the
// tracker's unavailable: exit 5, before any read.
func TestBacklogIssueModeWithoutProviderExits5(t *testing.T) {
	root := trackerProject(t, `{"backlog": {"backend": "issues"}}`)
	for _, argv := range [][]string{{"backlog", "list"}, {"backlog", "ids", "--milestone", "M01"}, {"backlog", "milestones", "12"}, {"summary"}} {
		code, env, stderr := rotaRun(t, append([]string{"--json", "-C", root}, argv...)...)
		if code != ExitUnavailable || env["ok"] != false || !strings.Contains(stderr, "provider") {
			t.Errorf("%v: code=%d stderr=%s", argv, code, stderr)
		}
	}
}

func TestBacklogFileOnlyVerbsAreRefusedUnderIssues(t *testing.T) {
	root := trackerProject(t, `{"backlog": {"backend": "issues"}}`)
	// drift is read-only, so its refusal is exit 1 (contract: backend).
	for _, c := range []struct {
		argv []string
		exit int
	}{{[]string{"backlog", "drift"}, ExitFailed}, {[]string{"backlog", "backfill"}, ExitRefused}, {[]string{"backlog", "archive"}, ExitRefused}} {
		argv := c.argv
		code, env, _ := rotaRun(t, append([]string{"--json", "-C", root}, argv...)...)
		d := dataOf(env)
		if code != c.exit || get(d, "blockedBy") != "backend" || get(d, "changed") != false {
			t.Errorf("%v: code=%d env=%v", argv, code, env)
		}
	}
}

func TestBacklogViewsInIssueMode(t *testing.T) {
	root := trackerProject(t, `{"backlog": {"backend": "issues"}}`)
	withTracker(t, &trackertest.Fake{Issues: []backlog.Issue{
		{Number: 12, Title: "Crash on save", State: "open", Labels: []string{"type:bug"}, Milestone: "M02 — Next",
			Body: "<!-- rota:fields\nRelated: F3\n-->"},
		{Number: 3, Title: "Dark mode", State: "open", Labels: []string{"type:feature"}, Body: "<!-- rota:fields\nRelated: B12\n-->"},
		{Number: 5, Title: "Chore", State: "open"},
	}})
	code, env, _ := rotaRun(t, "--json", "-C", root, "backlog", "list")
	if code != 0 {
		t.Fatalf("list: exit %d env=%v", code, env)
	}
	d := dataOf(env)
	bug := get(d, "bugs").([]any)[0]
	if get(bug, "id") != "12" || get(bug, "milestone") != "M02" || !reflect.DeepEqual(get(bug, "related"), []any{"3"}) {
		t.Errorf("bug row = %v", bug)
	}
	if got := get(d, "clusters").([]any); len(got) != 1 || !reflect.DeepEqual(got[0], []any{"12", "3"}) {
		t.Errorf("clusters = %v", got)
	}

	code, env, _ = rotaRun(t, "--json", "-C", root, "backlog", "ids", "--milestone", "M02")
	if code != 0 || !reflect.DeepEqual(get(dataOf(env), "ids"), []any{"12"}) {
		t.Errorf("ids: %d %v", code, env)
	}
	for _, ref := range []string{"12", "#12", "B12"} {
		code, env, _ = rotaRun(t, "--json", "-C", root, "backlog", "milestones", ref)
		if code != 0 || !reflect.DeepEqual(get(dataOf(env), "milestones"), []any{"M02"}) {
			t.Errorf("milestones %s: %d %v", ref, code, env)
		}
	}

	// Active streams use the issue number as the in-progress ID; its type
	// comes from the listing.
	os.WriteFile(filepath.Join(root, ".rota", "status.json"), []byte(`{"active": [{"branch": "b", "items": ["12"], "startedAt": "2026-09-01T10:00:00Z"}]}`), 0o644)
	_, env, _ = rotaRun(t, "--json", "-C", root, "backlog", "list")
	prog := get(dataOf(env), "inProgress").([]any)
	if len(prog) != 1 || get(prog[0], "id") != "12" || get(prog[0], "type") != "B" {
		t.Errorf("in progress = %v", prog)
	}
}

func TestBacklogUmbrellaFileModeScopes(t *testing.T) {
	root := trackerProject(t, "")
	os.WriteFile(filepath.Join(root, ".rota", "repos.json"), []byte(`{"repos": [{"name": "web", "path": "web"}]}`), 0o644)
	for _, argv := range [][]string{{"backlog", "list"}, {"summary"}, {"backlog", "ids", "--milestone", "M01"}} {
		if code, _, stderr := rotaRun(t, append([]string{"--json", "-C", root}, argv...)...); code != 0 {
			t.Errorf("%v in a file umbrella: exit %d: %s", argv, code, stderr)
		}
	}
	if code, _, _ := rotaRun(t, "--json", "-C", root, "backlog", "list", "--repo", "web"); code != 0 {
		t.Errorf("registered --repo: exit %d", code)
	}
	if code, _, _ := rotaRun(t, "--json", "-C", root, "backlog", "list", "--repo", "api"); code != ExitResolution {
		t.Errorf("unregistered --repo: exit %d, want 3", code)
	}
}

func TestBacklogUsageErrors(t *testing.T) {
	root := trackerProject(t, "")
	for _, argv := range [][]string{
		{"backlog", "ids"}, {"backlog", "milestones"}, {"backlog", "stale"}, {"backlog", "stale", "--kind", "plans"},
		{"backlog", "archive", "--days", "-1"}, {"status", "add", "b"}, {"status", "add", "b", "--items", "B01", "--repos", ""},
		{"status", "rm"}, {"status", "show"}, {"status", "handoff"},
		{"refactor", "targets", "--repo", "web"}, {"summary", "extra"},
	} {
		if code, env, _ := rotaRun(t, append([]string{"--json", "-C", root}, argv...)...); code != ExitUsage || env["ok"] != false {
			t.Errorf("%v: exit %d, want 2", argv, code)
		}
	}
}

func TestBacklogStaleBadTodayEnv(t *testing.T) {
	root := trackerProject(t, "")
	t.Setenv("ROTA_TEST_TODAY", "tomorrow")
	if code, _, stderr := rotaRun(t, "--json", "-C", root, "backlog", "stale", "--kind", "todo"); code != ExitUsage || !strings.Contains(stderr, "ROTA_TEST_TODAY") {
		t.Errorf("exit %d, stderr %s", code, stderr)
	}
	t.Setenv("ROTA_TEST_TODAY", "2999-01-01")
	os.WriteFile(filepath.Join(root, ".rota", "BACKLOG.md"), []byte("## Bugs\n- **[B01] [P1] a.** x Captured: 2026-01-01\n"), 0o644)
	code, env, _ := rotaRun(t, "--json", "-C", root, "backlog", "stale", "--kind", "todo", "--days", "90")
	entries := get(dataOf(env), "entries").([]any)
	if code != 0 || len(entries) != 1 || get(entries[0], "name") != "B01" || get(entries[0], "date") != "2026-01-01" {
		t.Errorf("stale todo: %d %v", code, env)
	}
}

func TestBacklogArchiveHonoursTestToday(t *testing.T) {
	root := trackerProject(t, "")
	os.WriteFile(filepath.Join(root, ".rota", "BACKLOG.md"), []byte("# TODO\n\n## Completed\n- ~~**[B01] [P1] a.** x~~ Done 2026-01-10 [`abc1234`]\n"), 0o644)
	t.Setenv("ROTA_TEST_TODAY", "2026-01-12")
	if code, env, _ := rotaRun(t, "--json", "-C", root, "backlog", "archive"); code != 0 || fmt.Sprint(get(dataOf(env), "moved")) != "0" {
		t.Fatalf("2 days old moved: %d %v", code, env)
	}
	t.Setenv("ROTA_TEST_TODAY", "2026-01-20")
	if code, env, _ := rotaRun(t, "--json", "-C", root, "backlog", "archive"); code != 0 || fmt.Sprint(get(dataOf(env), "moved")) != "1" {
		t.Fatalf("10 days old not moved: %d %v", code, env)
	}
	t.Setenv("ROTA_TEST_TODAY", "soon")
	if code, _, stderr := rotaRun(t, "--json", "-C", root, "backlog", "archive"); code != ExitUsage || !strings.Contains(stderr, "ROTA_TEST_TODAY") {
		t.Errorf("bad ROTA_TEST_TODAY: exit %d, stderr %s", code, stderr)
	}
}

func TestBacklogStatusLifecycle(t *testing.T) {
	root := trackerProject(t, "")
	run := func(argv ...string) (int, map[string]any) {
		code, env, _ := rotaRun(t, append([]string{"--json", "-C", root}, argv...)...)
		return code, env
	}
	code, env := run("status", "add", "feat/x", "--items", "B01,T01", "--worktree", "../wt")
	if code != 0 || get(dataOf(env), "changed") != true {
		t.Fatalf("add: %d %v", code, env)
	}
	e := get(dataOf(env), "entries").([]any)[0]
	if get(e, "repo") != nil || !reflect.DeepEqual(get(e, "items"), []any{"B01", "T01"}) || get(e, "worktree") != "../wt" {
		t.Errorf("entry = %v", e)
	}
	if code, env = run("status", "add", "feat/x", "--items", "B02", "--if-absent"); code != 0 || get(dataOf(env), "changed") != false {
		t.Errorf("if-absent: %d %v", code, env)
	}
	if _, env = run("status", "show", "feat/x"); get(dataOf(env), "active") != true || get(dataOf(env), "worktree") != "../wt" {
		t.Errorf("show: %v", env)
	}
	if _, env = run("status", "show", "nope"); get(dataOf(env), "active") != false || get(dataOf(env), "repo") != nil {
		t.Errorf("show inactive: %v", env)
	}
	hp := filepath.Join(root, ".rota", "handoff", "feat", "x.md")
	os.MkdirAll(filepath.Dir(hp), 0o755)
	os.WriteFile(hp, []byte("h"), 0o644)
	if _, env = run("status", "handoff", "feat/x"); get(dataOf(env), "path") != ".rota/handoff/feat/x.md" || get(dataOf(env), "exists") != true {
		t.Errorf("handoff: %v", env)
	}
	if code, env = run("status", "rm", "feat/x"); code != 0 || fmt.Sprint(get(dataOf(env), "removed")) != "1" || get(dataOf(env), "handoffRemoved") != true || get(dataOf(env), "changed") != true {
		t.Errorf("rm: %d %v", code, env)
	}
	if _, err := os.Stat(hp); err == nil {
		t.Error("handoff note survived rm")
	}
	if code, env = run("status", "rm", "feat/x"); code != 0 || get(dataOf(env), "changed") != false {
		t.Errorf("second rm: %d %v", code, env)
	}
}

func TestBacklogRefactorTargetsNeedsNoProject(t *testing.T) {
	dir := t.TempDir()
	code, env, _ := rotaRun(t, "--json", "-C", dir, "refactor", "targets")
	d := dataOf(env)
	if code != 0 || get(d, "umbrella") != nil || len(get(d, "subRepos").([]any)) != 0 {
		t.Errorf("no .rota: %d %v", code, env)
	}
	os.MkdirAll(filepath.Join(dir, ".rota"), 0o755)
	os.MkdirAll(filepath.Join(dir, "web"), 0o755)
	os.WriteFile(filepath.Join(dir, ".rota", "repos.json"), []byte(`{"repos": [{"name": "web", "path": "web"}]}`), 0o644)
	_, env, _ = rotaRun(t, "--json", "-C", dir, "refactor", "targets")
	if get(dataOf(env), "umbrella", "hasCode") != false {
		t.Errorf("only a sub-repo and .rota: %v", env)
	}
	os.WriteFile(filepath.Join(dir, "main.go"), nil, 0o644)
	_, env, _ = rotaRun(t, "--json", "-C", dir, "refactor", "targets")
	if get(dataOf(env), "umbrella", "hasCode") != true {
		t.Errorf("a top-level file: %v", env)
	}
}

func TestBacklogSummaryNoBacklogIsResolution(t *testing.T) {
	root := trackerProject(t, "")
	os.Remove(filepath.Join(root, ".rota", "BACKLOG.md"))
	if code, _, stderr := rotaRun(t, "--json", "-C", root, "summary"); code != ExitResolution || !strings.Contains(stderr, "rota init") {
		t.Errorf("exit %d stderr %s", code, stderr)
	}
}

func TestBacklogSummaryRecentIsNewestFirstInFileMode(t *testing.T) {
	root := trackerProject(t, "")
	md := "# TODO\n\n## Completed\n" +
		"- ~~**[B01] [P1] a.** x~~ Done 2026-10-01 [`aaa1111`]\n" +
		"- ~~**[B02] [P1] b.** x~~ Done 2026-10-03 [`bbb2222`]\n" +
		"- ~~**[B03] [P1] c.** x~~ Done 2026-10-01 [`ccc3333`]\n" +
		"- ~~**[B04] [P1] d.** x~~ Done 2026-10-04 [`ddd4444`]\n" +
		"- ~~**[B05] [P1] e.** x~~ Done 2026-10-03 [`eee5555`]\n"
	os.WriteFile(filepath.Join(root, ".rota", "BACKLOG.md"), []byte(md), 0o644)
	code, env, _ := rotaRun(t, "--json", "-C", root, "summary")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var got []string
	for _, r := range get(dataOf(env), "recent").([]any) {
		got = append(got, get(r, "id").(string))
	}
	if strings.Join(got, ",") != "B04,B05,B02" {
		t.Errorf("recent = %v, want B04,B05,B02", got)
	}
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	var out, errb bytes.Buffer
	Main([]string{"-C", root, "summary"}, strings.NewReader(""), &out, &errb)
	if !strings.Contains(out.String(), "Recent: [B04] on 2026-10-04, [B05] on 2026-10-03, [B02] on 2026-10-03") {
		t.Errorf("text output: %s", out.String())
	}
}
