package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/backlog/trackertest"
	"github.com/l4ci/rota/internal/tracker"
)

// a4dRepo is a project that is a git repo with the origin remote.
func a4dRepo(t *testing.T, origin string) string {
	t.Helper()
	root := a4Project(t, `{"issues": {"bulkPaceMs": 0}}`)
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"remote", "add", "origin", origin}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return root
}

// a4dForge scripts gh and glab for the verbs: calls collects them.
func a4dForge(t *testing.T, origin string, reply map[string]string) *[]string {
	t.Helper()
	var calls []string
	exe := func(_ context.Context, _ string, name string, args []string, _ []byte) ([]byte, []byte, int, error) {
		if name == "git" {
			return []byte(origin + "\n"), nil, 0, nil
		}
		line := strings.Join(args, " ")
		calls = append(calls, name+" "+line)
		for p, out := range reply {
			if strings.HasPrefix(line, p) {
				return []byte(out), nil, 0, nil
			}
		}
		return nil, nil, 0, nil
	}
	old := trackerOptions
	trackerOptions = []tracker.Option{tracker.WithExec(exe, func(n string) (string, error) { return "/fake/" + n, nil })}
	t.Cleanup(func() { trackerOptions = old })
	return &calls
}

func TestA4dProvider(t *testing.T) {
	for origin, want := range map[string]string{"https://github.com/o/r.git": "github", "git@gitlab.com:o/r.git": "gitlab", "https://example.org/r.git": "unknown"} {
		root := a4dRepo(t, origin)
		code, env, _ := rotaRun(t, "--json", "-C", root, "issues", "provider")
		if code != 0 || get(dataOf(env), "provider") != want {
			t.Errorf("%s: %d %v", origin, code, env)
		}
	}
	code, _, _ := rotaRun(t, "--json", "-C", t.TempDir(), "issues", "provider")
	if code != ExitResolution {
		t.Errorf("no project: %d", code)
	}
	root := a4dRepo(t, "https://github.com/o/r.git")
	if code, _, _ := rotaRun(t, "--json", "-C", root, "issues", "provider", "--repo", "web"); code != ExitResolution {
		t.Errorf("--repo outside umbrella: %d", code)
	}
}

func TestA4dListLabelClose(t *testing.T) {
	origin := "https://github.com/o/r.git"
	root := a4dRepo(t, origin)
	calls := a4dForge(t, origin, map[string]string{
		"issue list": `[{"number": 3, "title": "T", "body": "b", "labels": [{"name": "x"}], "url": "u", "author": {"login": "me"}}]`,
		"issue view": `{"labels": [{"name": "x"}]}`,
		"label list": `[{"name": "x"}]`,
	})
	code, env, _ := rotaRun(t, "--json", "-C", root, "issues", "list", "--label", "x", "--limit", "5")
	rows, _ := get(dataOf(env), "issues").([]any)
	if code != 0 || len(rows) != 1 || get(rows[0], "author") != "me" || get(rows[0], "title") != "T" {
		t.Fatalf("list: %d %v", code, env)
	}
	if !slices.Contains(*calls, "gh issue list --state open --json number,title,body,labels,milestone,state,stateReason,closedAt,url,assignees,author --label x --limit 1000") {
		t.Errorf("calls %q", *calls)
	}
	code, env, _ = rotaRun(t, "--json", "-C", root, "issues", "label", "3", "--add", "x")
	if code != 0 || get(dataOf(env), "changed") != false || get(dataOf(env), "action") != "add" {
		t.Errorf("label no-op: %d %v", code, env)
	}
	code, env, _ = rotaRun(t, "--json", "-C", root, "issues", "label", "3", "--remove", "x")
	if code != 0 || get(dataOf(env), "changed") != true {
		t.Errorf("label remove: %d %v", code, env)
	}
	// usage
	for _, argv := range [][]string{
		{"issues", "list", "--limit", "0"}, {"issues", "list", "--label", ""}, {"issues", "list", "7"},
		{"issues", "label", "3"}, {"issues", "label", "3", "--add", "a", "--remove", "b"}, {"issues", "label", "x", "--add", "a"}, {"issues", "label", "--add", "a"},
		{"issues", "close", "3"}, {"issues", "close", "3x", "--commit", "HEAD"}, {"issues", "close", "--commit", "HEAD"},
		{"issues", "imported", "x"}, {"migrate", "issues", "--limit", "x"}, {"migrate", "issues", "extra"},
	} {
		if code, _, _ := rotaRun(t, append([]string{"--json", "-C", root}, argv...)...); code != ExitUsage {
			t.Errorf("%v: exit %d, want usage", argv, code)
		}
	}
	// the commit is checked before the forge
	n := len(*calls)
	code, _, _ = rotaRun(t, "--json", "-C", root, "issues", "close", "3", "--commit", "deadbeef")
	if code != ExitResolution || len(*calls) != n {
		t.Errorf("unknown commit: %d, %d new calls", code, len(*calls)-n)
	}
}

func TestA4dImported(t *testing.T) {
	root := a4Project(t, "{}\n")
	os.WriteFile(filepath.Join(root, ".rota", "BACKLOG.md"), []byte("# T\n\n## Bugs\n- **[B01] [P1] A.** GH: #5 Repos: web\n"), 0o644)
	code, env, _ := rotaRun(t, "--json", "-C", root, "issues", "imported")
	rows, _ := get(dataOf(env), "entries").([]any)
	if code != 0 || len(rows) != 1 || get(rows[0], "itemId") != "B01" || get(rows[0], "repo") != "web" || get(rows[0], "status") != "open" {
		t.Errorf("%d %v", code, env)
	}
	if code, env, _ := rotaRun(t, "--json", "-C", root, "issues", "imported", "--for-repo", "api"); code != 0 || len(get(dataOf(env), "entries").([]any)) != 0 {
		t.Errorf("filter: %d %v", code, env)
	}
	// --open-only asks the forge; a missing CLI drops the entry, exit stays 0
	old := trackerOptions
	trackerOptions = []tracker.Option{tracker.WithExec(nil, func(string) (string, error) { return "", exec.ErrNotFound })}
	defer func() { trackerOptions = old }()
	if code, env, _ := rotaRun(t, "--json", "-C", root, "issues", "imported", "--open-only"); code != 0 || len(get(dataOf(env), "entries").([]any)) != 0 {
		t.Errorf("open-only: %d %v", code, env)
	}
	// the repo flag belongs to the other verbs
	if code, _, _ := rotaRun(t, "--json", "-C", root, "issues", "imported", "--repo", "web"); code != ExitUsage {
		t.Errorf("--repo: %d", code)
	}
}

// msStub is the fake forge with the milestone calls the migration needs.
type msStub struct {
	*trackertest.Fake
	fail error
}

func (s *msStub) Milestones(context.Context, string) ([]tracker.Milestone, error) { return nil, nil }
func (s *msStub) CreateMilestone(context.Context, string, string) (int, error)    { return 1, nil }
func (s *msStub) EditMilestone(context.Context, int, tracker.MilestoneEdit) error { return nil }
func (s *msStub) Create(ctx context.Context, title, body string, labels []string, ms string) (int, error) {
	if s.fail != nil {
		return 0, s.fail
	}
	return s.Fake.Create(ctx, title, body, labels, ms)
}

func TestA4dMigrateIssues(t *testing.T) {
	root := a4dRepo(t, "https://github.com/o/r.git")
	stub := &msStub{Fake: &trackertest.Fake{}}
	old := migrateTracker
	migrateTracker = func(context.Context, string, any) (backlog.MigrateTracker, error) { return stub, nil }
	defer func() { migrateTracker = old }()

	code, env, stderr := rotaRun(t, "--json", "-C", root, "migrate", "issues")
	d := dataOf(env)
	ops, _ := get(d, "operations").([]any)
	if code != 0 || get(d, "applied") != false || len(ops) != 1 || get(ops[0], "action") != "create-issue" || len(stub.Issues) != 0 {
		t.Fatalf("preview: %d %v", code, env)
	}
	if !strings.Contains(stderr, "preview only; pass --apply") {
		t.Errorf("stderr %q", stderr)
	}
	if ws, _ := env["warnings"].([]any); len(ws) != 1 {
		t.Errorf("warnings %v", env["warnings"])
	}

	// a rate limit and a failure keep their exits, and the message ends with the progress
	stub.fail = &tracker.Error{Kind: tracker.KindRateLimited, Code: 4, Message: "slow"}
	code, env, _ = rotaRun(t, "--json", "-C", root, "migrate", "issues", "--apply")
	if msg, _ := get(env, "error", "message").(string); code != ExitRetry || msg != "0 of 1 migrated" {
		t.Errorf("rate limit: %d %v", code, env)
	}
	stub.fail = &tracker.Error{Kind: tracker.KindFailed, Code: 1, Message: "boom"}
	code, env, _ = rotaRun(t, "--json", "-C", root, "migrate", "issues", "--apply")
	if msg, _ := get(env, "error", "message").(string); code != ExitUnavailable || msg != "boom; 0 of 1 migrated" {
		t.Errorf("failure: %d %v", code, env)
	}
	if _, has := env["warnings"]; has || env["data"] != nil {
		t.Errorf("a failure carries no data: %v", env)
	}

	stub.fail = nil
	code, env, _ = rotaRun(t, "--json", "-C", root, "migrate", "issues", "--apply")
	d = dataOf(env)
	if code != 0 || get(d, "changed") != true || get(d, "migrated").(interface{ String() string }).String() != "1" || len(stub.Issues) != 1 {
		t.Fatalf("apply: %d %v", code, env)
	}
	if b, _ := os.ReadFile(filepath.Join(root, ".rota", "BACKLOG.md")); !strings.HasPrefix(string(b), "> Frozen:") {
		t.Error("not frozen")
	}
	code, env, _ = rotaRun(t, "--json", "-C", root, "migrate", "issues", "--apply")
	if code != 0 || get(dataOf(env), "changed") != false {
		t.Errorf("rerun: %d %v", code, env)
	}

	// refusals
	if code, _, _ := rotaRun(t, "--json", "-C", t.TempDir(), "migrate", "issues"); code != ExitResolution {
		t.Errorf("no project: %d", code)
	}
	empty := a4Project(t, "{}\n")
	os.Remove(filepath.Join(empty, ".rota", "BACKLOG.md"))
	if code, _, _ := rotaRun(t, "--json", "-C", empty, "migrate", "issues"); code != ExitResolution {
		t.Errorf("no backlog: %d", code)
	}
	umb := a4Project(t, "{}\n")
	os.WriteFile(filepath.Join(umb, ".rota", "repos.json"), []byte(`{"repos": [{"name": "web", "path": "web"}]}`), 0o644)
	code, env, _ = rotaRun(t, "--json", "-C", umb, "migrate", "issues")
	if code != ExitRefused || get(dataOf(env), "blockedBy") != "umbrella" || get(dataOf(env), "changed") != false {
		t.Errorf("umbrella: %d %v", code, env)
	}
}

// Without --repo the verbs act on the sub-repo the working directory is in
// (scope S); --repo wins, and the umbrella root has no origin of its own.
func TestA4dScopeFollowsWorkingDirectory(t *testing.T) {
	root := a4Project(t, "{}\n")
	for name, origin := range map[string]string{"web": "https://github.com/o/web.git", "api": "https://gitlab.com/o/api.git"} {
		dir := filepath.Join(root, name)
		os.MkdirAll(filepath.Join(dir, "src"), 0o755)
		for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"remote", "add", "origin", origin}} {
			cmd := exec.Command("git", args...)
			cmd.Dir = dir
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
	}
	os.WriteFile(filepath.Join(root, ".rota", "repos.json"), []byte(`{"repos": [{"name": "web", "path": "web"}, {"name": "api", "path": "api"}]}`), 0o644)
	for _, c := range []struct {
		argv []string
		want string
	}{
		{[]string{"-C", root, "issues", "provider"}, "unknown"},
		{[]string{"-C", filepath.Join(root, "web", "src"), "issues", "provider"}, "github"},
		{[]string{"-C", filepath.Join(root, "web"), "issues", "provider", "--repo", "api"}, "gitlab"},
		{[]string{"-C", root, "issues", "provider", "--repo", "web"}, "github"},
	} {
		code, env, _ := rotaRun(t, append([]string{"--json"}, c.argv...)...)
		if code != 0 || get(dataOf(env), "provider") != c.want {
			t.Errorf("%v: %d %v, want %s", c.argv, code, env, c.want)
		}
	}
}

func TestA4dNoProviderAndMissingIssue(t *testing.T) {
	// no origin, no issues.provider: list and label exit 3 instead of an empty list
	root := a4dRepo(t, "")
	a4dForge(t, "", nil)
	for _, argv := range [][]string{{"issues", "list"}, {"issues", "label", "3", "--add", "x"}} {
		code, env, _ := rotaRun(t, append([]string{"--json", "-C", root}, argv...)...)
		if msg, _ := get(env, "error", "message").(string); code != ExitResolution || !strings.Contains(msg, "issues.provider") {
			t.Errorf("%v: %d %v", argv, code, env)
		}
	}
	// issues.provider stands in for a missing origin
	root = a4dRepo(t, "")
	os.WriteFile(filepath.Join(root, ".rota", "config.json"), []byte(`{"issues": {"provider": "github"}}`), 0o644)
	calls := a4dForge(t, "", map[string]string{"issue list": "[]"})
	if code, env, _ := rotaRun(t, "--json", "-C", root, "issues", "list"); code != 0 || !slices.Contains(*calls, "gh issue list --state open --json number,title,body,labels,milestone,state,stateReason,closedAt,url,assignees,author --limit 1000") {
		t.Errorf("config fallback: %d %v %q", code, env, *calls)
	}
	// a missing issue is exit 3; another forge failure stays exit 5
	root = a4dRepo(t, "https://github.com/o/r.git")
	for stderr, want := range map[string]int{
		"GraphQL: Could not resolve to an Issue with the number of 99.": ExitResolution,
		"HTTP 500: server error": ExitUnavailable,
	} {
		exe := func(_ context.Context, _ string, name string, args []string, _ []byte) ([]byte, []byte, int, error) {
			if name == "git" {
				return []byte("https://github.com/o/r.git\n"), nil, 0, nil
			}
			if len(args) > 1 && args[0] == "issue" && args[1] == "edit" {
				return nil, []byte(stderr), 1, nil
			}
			return nil, nil, 0, nil
		}
		old := trackerOptions
		trackerOptions = []tracker.Option{tracker.WithExec(exe, func(n string) (string, error) { return "/fake/" + n, nil })}
		code, env, _ := rotaRun(t, "--json", "-C", root, "issues", "label", "99", "--remove", "x")
		trackerOptions = old
		if code != want {
			t.Errorf("%q: exit %d, want %d: %v", stderr, code, want, env)
		}
	}
}
