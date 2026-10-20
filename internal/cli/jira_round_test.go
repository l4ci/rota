package cli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/connections"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/tracker/jiratest"
	"github.com/l4ci/rota/internal/worker"
)

const jiraRoundCfg = `{"forge":{"provider":"github"},"issues":{"source":"jira","connection":"work","jira":{"project":"PROJ"}},"round":{"scope":"open"}}`

// jiraFx is a round repo whose issue source is the in-process fake Jira; the
// forge stays github behind newScopeFx's scripted gh.
type jiraFx struct {
	*scopeFx
	s    *jiratest.Server
	it   tracker.IssueTracker
	conn tracker.Option
}

// claims reads the claim comments of key on the fake.
func (f *jiraFx) claims(key string) ([]string, error) {
	return f.it.(tracker.ClaimStore).Claims(context.Background(), tracker.IssueKey(key))
}

func newJiraFx(t *testing.T, slots ...string) *jiraFx {
	t.Helper()
	fx := newScopeFx(t, "warn", "")
	s := jiratest.New(t, jiratest.Options{})
	conn := tracker.WithConnections(func(_ context.Context, name string) (connections.Connection, string, error) {
		return connections.Connection{Name: name, Kind: connections.KindJira, Site: s.URL, User: "me@example.com", Deployment: connections.DeploymentCloud}, "tok", nil
	})
	fx.deps.TrackerOptions = append(fx.deps.TrackerOptions, conn)
	write(t, filepath.Join(fx.root, ".rota", "config.json"), jiraRoundCfg)
	// every slot is idle on its parked worktree (ben's github task is dropped)
	var rows []string
	for _, n := range slots {
		wt := filepath.Join(t.TempDir(), n)
		if n != "dana" { // newScopeFx already made dana's branch
			if out, err := exec.Command("git", "-C", fx.root, "worktree", "add", "-q", "-b", "park/"+n, wt).CombinedOutput(); err != nil {
				t.Fatalf("worktree: %v %s", err, out)
			}
		} else {
			wt = worktreeOf(t, fx.root, "park/dana")
		}
		rows = append(rows, `{"name":"`+n+`","branch":"park/`+n+`","base":"feat/x","worktree":"`+wt+`"}`)
	}
	write(t, filepath.Join(fx.root, ".rota", "workers.json"),
		`{"architectureReview":{"at":"2020-01-01T00:00:00Z"},"slotCount":4,"slots":[`+strings.Join(rows, ",")+`]}`)
	cfg, err := jsonx.Decode([]byte(jiraRoundCfg))
	if err != nil {
		t.Fatal(err)
	}
	it, err := tracker.NewIssueTrackerFromConfig(context.Background(), cfg, fx.root, conn)
	if err != nil {
		t.Fatal(err)
	}
	return &jiraFx{scopeFx: fx, s: s, it: it, conn: conn}
}

func worktreeOf(t *testing.T, root, branch string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", root, "worktree", "list", "--porcelain").Output()
	if err != nil {
		t.Fatal(err)
	}
	var path string
	for _, l := range strings.Split(string(out), "\n") {
		if p, ok := strings.CutPrefix(l, "worktree "); ok {
			path = p
		}
		if l == "branch refs/heads/"+branch {
			return path
		}
	}
	t.Fatalf("no worktree on %s", branch)
	return ""
}

func (f *jiraFx) create(t *testing.T, title, extra string) tracker.IssueKey {
	t.Helper()
	k, err := f.it.Create(context.Background(), title, "## Acceptance\n- [ ] works\n\n"+extra, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func (f *jiraFx) assign(t *testing.T, key, slot string, extra ...string) (int, string, string) {
	t.Helper()
	args := append([]string{"--json", "round", "assign", key, "--agent", slot, "--holder-pid", "4242"}, extra...)
	return rotaInWith(t, f.deps, f.root, args...)
}

// AC-2: `round assign` on a Jira key claims by issue property, labels and
// assigns the issue, branches from the key, and leaves no marker comment.
func TestJiraRoundAssignClaimsByProperty(t *testing.T) {
	f := newJiraFx(t, "dana")
	key := f.create(t, "Widen the seam", "")
	if key != "PROJ-1" {
		t.Fatalf("key %s", key)
	}
	code, out, errOut := f.assign(t, "PROJ-1", "dana")
	if code != 0 {
		t.Fatalf("assign: %d %s %s", code, out, errOut)
	}
	if b, _ := data(t, out)["branch"].(string); !strings.HasPrefix(b, "dana/PROJ-1-") {
		t.Errorf("branch %q", b)
	}
	if s := worker.LoadRegistryTolerant(f.root).Slot("dana"); s.Task() != "PROJ-1" {
		t.Errorf("slot task %q", s.Task())
	}
	if cs, err := f.claims("PROJ-1"); err != nil || !slices.Equal(cs, []string{"dana@0"}) {
		t.Errorf("claims %v, %v; requests:\n%s", cs, err, strings.Join(f.s.Requests(), "\n"))
	}
	is, err := f.it.Get(context.Background(), "PROJ-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(is.Labels, "in-progress") {
		t.Errorf("labels %v", is.Labels)
	}
	if len(is.Assignees) == 0 {
		t.Errorf("not assigned: %+v", is)
	}
	for _, c := range f.s.CommentBodies("PROJ-1") {
		if strings.Contains(c, "<!--") || strings.Contains(c, "rota:") {
			t.Errorf("marker comment: %q", c)
		}
	}
}

// Two Jira issues naming the same tracked file overlap; the second
// assign is refused while the first is in flight.
func TestJiraRoundAssignOverlapBlocks(t *testing.T) {
	f := newJiraFx(t, "dana", "kit")
	os.MkdirAll(filepath.Join(f.root, "internal"), 0o755)
	write(t, filepath.Join(f.root, "internal", "seam.go"), "x")
	gitT(t, f.root, "add", "internal/seam.go")
	gitT(t, f.root, "-c", "user.email=a@b", "-c", "user.name=n", "commit", "-q", "-m", "seam")
	f.create(t, "First", "edits internal/seam.go")
	f.create(t, "Second", "also edits internal/seam.go")
	if code, out, errOut := f.assign(t, "PROJ-1", "dana"); code != 0 {
		t.Fatalf("first: %d %s %s", code, out, errOut)
	}
	code, out, errOut := f.assign(t, "PROJ-2", "kit")
	if code == 0 || !strings.Contains(out+errOut, "overlap") || !strings.Contains(out+errOut, "PROJ-1") {
		t.Fatalf("second should be blocked by PROJ-1: %d %s %s", code, out, errOut)
	}
	if cs, _ := f.claims("PROJ-2"); len(cs) != 0 {
		t.Errorf("the blocked issue was claimed: %v", cs)
	}
}

// prForge is the gate's forge for one PR; the gate calls nothing else on a
// check-only run, and an embedded nil interface panics if that changes.
type prForge struct {
	worker.Forge
	title string
	sha   string
}

func (f prForge) PRView(context.Context, int) (tracker.PRInfo, error) {
	return tracker.PRInfo{Head: "rota-worker/w1", HeadSHA: f.sha, Base: "main", State: "OPEN", Title: f.title}, nil
}

func (prForge) PRMergeable(context.Context, int) (tracker.Mergeability, error) {
	return tracker.Mergeability{State: tracker.MergeClean}, nil
}

func (prForge) ClosedNumbers(string) []int { return nil }

// AC-2: `worker gate` builds its issue side from issues.source jira. A PR
// that does not name the key is refused through the Jira issue (its
// partial-slice label is read there); naming the key in the title passes.
func TestJiraWorkerGate(t *testing.T) {
	dir := workerProject(t, `{"ship":{"review":"none"},"forge":{"provider":"github"},"issues":{"source":"jira","connection":"work","jira":{"project":"PROJ"}}}`)
	origin := filepath.Join(t.TempDir(), "origin.git")
	gitT(t, dir, "init", "-q", "--bare", "-b", "main", origin)
	gitT(t, dir, "remote", "add", "origin", origin)
	gitT(t, dir, "push", "-q", "origin", "main")
	deps := testDeps()
	rotaInWith(t, deps, dir, "worker", "pool", "init", "--slots", "1", "--base", "main")
	gitT(t, dir, "push", "-q", "origin", "rota-worker/w1")
	doc := registryDoc(t, dir)
	slot := doc["slots"].([]any)[0].(map[string]any)
	slot["task"], slot["pr"] = "PROJ-1", "https://github.com/o/r/pull/42"
	b, _ := json.Marshal(doc)
	write(t, filepath.Join(dir, ".rota", "workers.json"), string(b))

	s := jiratest.New(t, jiratest.Options{})
	conn := tracker.WithConnections(func(_ context.Context, name string) (connections.Connection, string, error) {
		return connections.Connection{Name: name, Kind: connections.KindJira, Site: s.URL, User: "me@example.com", Deployment: connections.DeploymentCloud}, "tok", nil
	})
	deps.TrackerOptions = append(deps.TrackerOptions, conn)
	cfg, err := jsonx.Decode([]byte(`{"issues":{"source":"jira","connection":"work","jira":{"project":"PROJ"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	it, err := tracker.NewIssueTrackerFromConfig(context.Background(), cfg, dir, conn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := it.Create(context.Background(), "Widen", "## Acceptance\n- [ ] ok\n", nil, ""); err != nil {
		t.Fatal(err)
	}
	sha := strings.TrimSpace(gitT(t, dir, "rev-parse", "origin/rota-worker/w1"))
	title := "unrelated change"
	deps.WorkerEnv = func() worker.Env {
		return worker.Env{Forge: func(string, string, tracker.Settings) (worker.Forge, error) {
			return prForge{title: title, sha: sha}, nil
		}}
	}
	gate := func() (int, map[string]any, string) {
		code, out, errOut := rotaInWith(t, deps, dir, "--json", "worker", "gate", "w1", "--base", "main", "--check-only")
		return code, data(t, out), errOut
	}

	code, d, errOut := gate()
	if code != 4 || d["verdict"] != "closes" || !strings.Contains(errOut, "PROJ-1") {
		t.Fatalf("unrelated title: %d %v %s", code, d, errOut)
	}
	if !slices.ContainsFunc(s.Requests(), func(r string) bool { return strings.HasPrefix(r, "GET /rest/api/3/issue/PROJ-1") }) {
		t.Errorf("the gate never read PROJ-1 from Jira:\n%s", strings.Join(s.Requests(), "\n"))
	}

	title = "PROJ-1 widen the seam"
	if code, d, errOut = gate(); code != 0 {
		t.Fatalf("title names the key but the check failed: %d %v %s", code, d, errOut)
	}
}
