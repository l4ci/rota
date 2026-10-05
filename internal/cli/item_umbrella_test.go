package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/backlog/trackertest"
)

// umbrellaProject is an issue-mode umbrella with git sub-repos web and api and
// one fake tracker each, served by the injected NewTracker (keyed by the
// directory the tracker is built for).
func umbrellaProject(t *testing.T) (root string, deps *Deps, fakes map[string]*trackertest.Fake, built map[string]int) {
	t.Helper()
	root = trackerProject(t, `{"backlog": {"backend": "issues"}, "issues": {"homeRepo": "web"}}`)
	root, _ = filepath.EvalSymlinks(root)
	fakes = map[string]*trackertest.Fake{
		"web": {Issues: []backlog.Issue{
			{Number: 1, Title: "Web feat", Labels: []string{"type:feature"}, State: "open"},
			{Number: 2, Title: "Web bug", Labels: []string{"type:bug"}, State: "open"},
			{Number: 9, Title: "M07 — Title", Labels: []string{"milestone-tracker"}, State: "open"},
		}, Milestones: []string{"M07 — Title"}},
		"api": {Issues: []backlog.Issue{
			{Number: 1, Title: "Api feat", Labels: []string{"type:feature"}, State: "open"},
		}},
	}
	built = map[string]int{}
	var reg []string
	for _, name := range []string{"web", "api"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
			t.Fatalf("git init: %v\n%s", err, out)
		}
		reg = append(reg, `{"name": "`+name+`", "path": "`+name+`"}`)
	}
	if err := os.WriteFile(filepath.Join(root, ".rota", "repos.json"), []byte(`{"repos": [`+strings.Join(reg, ",")+`]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	deps = testDeps()
	deps.NewTracker = func(_ context.Context, dir string, _ any) (backlog.Tracker, error) {
		name := filepath.Base(dir)
		built[name]++
		return fakes[name], nil
	}
	return root, deps, fakes, built
}

func TestUmbrellaIssueRefs(t *testing.T) {
	root, deps, _, built := umbrellaProject(t)
	code, env, stderr := rotaRunWith(t, deps, "--json", "-C", root, "item", "field", "get", "api:F1", "--name", "title")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	d := issueData(t, env)
	if issueGet(d, "id") != "api:1" || issueGet(d, "type") != "F" || issueGet(d, "value") != "Api feat" {
		t.Errorf("data %v", d)
	}
	if built["web"] != 0 {
		t.Errorf("a qualified ref built web's tracker: %v", built)
	}
	// bare and ambiguous
	code, _, stderr = rotaRunWith(t, deps, "--json", "-C", root, "item", "field", "get", "F1", "--name", "title")
	if code != ExitUsage || !strings.Contains(stderr, "web:1, api:1") {
		t.Errorf("ambiguous: exit %d, %s", code, stderr)
	}
	code, env, _ = rotaRunWith(t, deps, "--json", "-C", root, "item", "field", "get", "B2", "--name", "title")
	if code != 0 || issueGet(issueData(t, env), "id") != "web:2" {
		t.Errorf("unique bare: exit %d %v", code, env)
	}
	for _, ref := range []string{"nope:F1", "api:99", "F99"} {
		if code, _, _ := rotaRunWith(t, deps, "--json", "-C", root, "item", "field", "get", ref, "--name", "title"); code != ExitResolution {
			t.Errorf("%s: exit %d, want 3", ref, code)
		}
	}
}

func TestUmbrellaRepoFlagNarrows(t *testing.T) {
	root, deps, _, _ := umbrellaProject(t)
	code, env, stderr := rotaRunWith(t, deps, "--json", "-C", root, "item", "field", "get", "F1", "--name", "title", "--repo", "web")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if d := issueData(t, env); issueGet(d, "id") != "web:1" || issueGet(d, "value") != "Web feat" {
		t.Errorf("data %v", d)
	}
	if code, _, _ := rotaRunWith(t, deps, "--json", "-C", root, "item", "field", "get", "B2", "--name", "title", "--repo", "api"); code != ExitResolution {
		t.Errorf("web's B2 outside --repo api: exit %d, want 3", code)
	}
	if code, _, _ := rotaRunWith(t, deps, "--json", "-C", root, "item", "field", "get", "F1", "--name", "title", "--repo", "nope"); code != ExitResolution {
		t.Errorf("unregistered --repo: exit %d, want 3", code)
	}
}

func TestUmbrellaCreateTarget(t *testing.T) {
	root, deps, fakes, built := umbrellaProject(t)
	create := func(root string, extra ...string) (int, string, string) {
		args := append([]string{"--json", "-C", root, "item", "create", "--kind", "tasks", "--title", "New"}, extra...)
		code, env, stderr := rotaRunWith(t, deps, args...)
		id := ""
		if code == 0 {
			id, _ = issueGet(issueData(t, env), "id").(string)
		}
		return code, id, stderr
	}
	for _, c := range []struct {
		name  string
		cwd   string
		extra []string
		want  int
		id    string
	}{
		{"repos field", "", []string{"--repos", "api"}, 0, "api:2"},
		{"repo flag", "", []string{"--repo", "web"}, 0, "web:10"},
		{"cwd", "web", nil, 0, "web:11"},
		{"deep cwd", "api/src", nil, 0, "api:3"},
		{"field wins over cwd", "web", []string{"--repos", "api"}, 0, "api:4"},
		{"no target", "", nil, ExitResolution, ""},
		{"several", "", []string{"--repos", "web,api"}, ExitResolution, ""},
		{"unknown repo", "", []string{"--repos", "nope"}, ExitResolution, ""},
		{"flag and field differ", "", []string{"--repo", "web", "--repos", "api"}, ExitUsage, ""},
		{"unregistered flag", "", []string{"--repo", "nope"}, ExitResolution, ""},
	} {
		dir := filepath.Join(root, c.cwd)
		code, id, stderr := create(dir, c.extra...)
		if code != c.want || id != c.id {
			t.Errorf("%s: exit %d id %q (%s), want %d %q", c.name, code, id, strings.TrimSpace(stderr), c.want, c.id)
		}
	}
	if len(fakes["web"].Issues) != 5 || len(fakes["api"].Issues) != 4 {
		t.Errorf("stores: web %d, api %d", len(fakes["web"].Issues), len(fakes["api"].Issues))
	}
	_ = built
}

func TestUmbrellaFileModeKeepsOneBacklog(t *testing.T) {
	root := trackerProject(t, "")
	os.WriteFile(filepath.Join(root, ".rota", "repos.json"), []byte(`{"repos": [{"name": "web", "path": "web"}]}`), 0o644)
	if code, env, stderr := rotaRun(t, "--json", "-C", root, "item", "create", "--kind", "bugs", "--title", "Umbrella bug", "--repos", "web", "--repo", "web"); code != 0 {
		t.Fatalf("exit %d: %s %v", code, stderr, env)
	}
	b, _ := os.ReadFile(filepath.Join(root, ".rota", "BACKLOG.md"))
	if !strings.Contains(string(b), "Umbrella bug.** Repos: web") {
		t.Errorf("BACKLOG.md:\n%s", b)
	}
	if code, _, _ := rotaRun(t, "--json", "-C", root, "item", "complete", "B01", "--commit", "abc1234", "--no-proof", "--repo", "nope"); code != ExitResolution {
		t.Errorf("unregistered --repo: exit %d, want 3", code)
	}
}

func TestUmbrellaFileOnlyVerbsRefused(t *testing.T) {
	root, deps, _, built := umbrellaProject(t)
	for _, argv := range [][]string{{"id", "next", "--kind", "bugs"}, {"item", "rm", "web:F1"}, {"backlog", "archive"}} {
		if code, env, _ := rotaRunWith(t, deps, append([]string{"--json", "-C", root}, argv...)...); code != ExitRefused || env["ok"] != false {
			t.Errorf("%v: exit %d", argv, code)
		}
	}
	if len(built) != 0 {
		t.Errorf("a refused verb built trackers: %v", built)
	}
}

func TestUmbrellaBacklogListQualifiesIDs(t *testing.T) {
	root, deps, _, _ := umbrellaProject(t)
	code, env, stderr := rotaRunWith(t, deps, "--json", "-C", root, "backlog", "list")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	d := issueData(t, env)
	var ids []string
	for _, k := range []string{"bugs", "features", "tasks"} {
		rows, _ := issueGet(d, k).([]any)
		for _, r := range rows {
			o := r.(interface{ Get(string) (any, bool) })
			v, _ := o.Get("id")
			ids = append(ids, v.(string))
		}
	}
	if got := strings.Join(ids, " "); got != "web:2 web:1 api:1" {
		t.Errorf("ids %q", got)
	}
	code, env, _ = rotaRunWith(t, deps, "--json", "-C", root, "backlog", "list", "--repo", "api")
	if code != 0 || len(issueGet(issueData(t, env), "features").([]any)) != 1 {
		t.Errorf("--repo api: exit %d %v", code, env)
	}
}
