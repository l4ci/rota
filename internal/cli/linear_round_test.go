package cli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/connections"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/tracker/lineartest"
	"github.com/l4ci/rota/internal/worker"
)

const linearCfg = `{"issues":{"source":"linear","connection":"work","linear":{"team":"ENG"}}}`

// linearSource is the fake Linear behind a connection option, and the issue
// tracker a test seeds it through.
func linearSource(t *testing.T, dir string) (*lineartest.Server, tracker.Option, tracker.IssueTracker) {
	t.Helper()
	s := lineartest.New(t, lineartest.Options{})
	conn := tracker.WithConnections(func(_ context.Context, name string) (connections.Connection, string, error) {
		return connections.Connection{Name: name, Kind: connections.KindLinear, Site: s.URL}, "lin_api_test", nil
	})
	cfg, err := jsonx.Decode([]byte(linearCfg))
	if err != nil {
		t.Fatal(err)
	}
	it, err := tracker.NewIssueTrackerFromConfig(context.Background(), cfg, dir, conn)
	if err != nil {
		t.Fatal(err)
	}
	return s, conn, it
}

// AC-2: `round assign` on a Linear identifier claims by a plain comment,
// labels and assigns the issue, branches from the identifier, and leaves no
// marker in any comment.
func TestLinearRoundAssign(t *testing.T) {
	fx := newScopeFx(t, "warn", "")
	s, conn, it := linearSource(t, fx.root)
	fx.deps.TrackerOptions = append(fx.deps.TrackerOptions, conn)
	write(t, filepath.Join(fx.root, ".rota", "config.json"), `{"forge":{"provider":"github"},"issues":{"source":"linear","connection":"work","linear":{"team":"ENG"}},"round":{"scope":"open"}}`)
	wt := worktreeOf(t, fx.root, "park/dana")
	write(t, filepath.Join(fx.root, ".rota", "workers.json"),
		`{"architectureReview":{"at":"2020-01-01T00:00:00Z"},"slots":[{"name":"dana","branch":"park/dana","base":"feat/x","worktree":"`+wt+`"}]}`)
	key, err := it.Create(context.Background(), "Widen the seam", "## Acceptance\n- [ ] works\n", nil, "")
	if err != nil || key != "ENG-1" {
		t.Fatal(key, err)
	}

	code, out, errOut := rotaInWith(t, fx.deps, fx.root, "--json", "round", "assign", "ENG-1", "--agent", "dana", "--holder-pid", "4242")
	if code != 0 {
		t.Fatalf("assign: %d %s %s", code, out, errOut)
	}
	if b, _ := data(t, out)["branch"].(string); !strings.HasPrefix(b, "dana/ENG-1-") {
		t.Errorf("branch %q", b)
	}
	if task := worker.LoadRegistryTolerant(fx.root).Slot("dana").Task(); task != "ENG-1" {
		t.Errorf("slot task %q", task)
	}
	if cs, err := it.(tracker.ClaimStore).Claims(context.Background(), "ENG-1"); err != nil || !slices.Equal(cs, []string{"dana@0"}) {
		t.Errorf("claims %v, %v; requests %v", cs, err, s.Requests())
	}
	if !slices.Contains(s.Labels("ENG-1"), "in-progress") || s.Assignee("ENG-1") == "" {
		t.Errorf("labels %v, assignee %q", s.Labels("ENG-1"), s.Assignee("ENG-1"))
	}
	for _, c := range s.CommentBodies("ENG-1") {
		if strings.Contains(c, "<!--") || strings.Contains(c, "rota:") {
			t.Errorf("marker comment: %q", c)
		}
	}
}

// AC-2: `worker gate` builds its issue side from issues.source linear. A PR
// that does not name the identifier is refused; naming it in the title passes.
func TestLinearWorkerGate(t *testing.T) {
	dir := workerProject(t, `{"ship":{"review":"none"},"forge":{"provider":"github"},"issues":{"source":"linear","connection":"work","linear":{"team":"ENG"}}}`)
	origin := filepath.Join(t.TempDir(), "origin.git")
	gitT(t, dir, "init", "-q", "--bare", "-b", "main", origin)
	gitT(t, dir, "remote", "add", "origin", origin)
	gitT(t, dir, "push", "-q", "origin", "main")
	deps := testDeps()
	rotaInWith(t, deps, dir, "worker", "pool", "init", "--slots", "1", "--base", "main")
	gitT(t, dir, "push", "-q", "origin", "rota-worker/w1")
	doc := registryDoc(t, dir)
	slot := doc["slots"].([]any)[0].(map[string]any)
	slot["task"], slot["pr"] = "ENG-1", "https://github.com/o/r/pull/42"
	b, _ := json.Marshal(doc)
	write(t, filepath.Join(dir, ".rota", "workers.json"), string(b))

	s, conn, it := linearSource(t, dir)
	deps.TrackerOptions = append(deps.TrackerOptions, conn)
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
	before := len(s.Requests())
	if code, d, errOut := gate(); code != 4 || d["verdict"] != "closes" || !strings.Contains(errOut, "ENG-1") {
		t.Fatalf("unrelated title: %d %v %s", code, d, errOut)
	}
	if !slices.Contains(s.Requests()[before:], "Issue") {
		t.Errorf("the gate never read ENG-1 from Linear: %v", s.Requests()[before:])
	}
	title = "ENG-1 widen the seam"
	if code, d, errOut := gate(); code != 0 {
		t.Fatalf("title names the identifier but the check failed: %d %v %s", code, d, errOut)
	}
}

// AC-3: doctor's team check reads the team through the connection; a team the
// key cannot see is the problem it reports.
func TestLinearTeamProblem(t *testing.T) {
	dir := t.TempDir()
	_, conn, _ := linearSource(t, dir)
	deps := testDeps()
	deps.TrackerOptions = append(deps.TrackerOptions, conn)
	for team, want := range map[string]string{"ENG": "", "NOPE": "team NOPE is not visible"} {
		cfg, _ := jsonx.Decode([]byte(strings.Replace(linearCfg, `"ENG"`, `"`+team+`"`, 1)))
		got := linearTeamProblem(context.Background(), deps, cfg, dir)
		if want == "" && got != "" || !strings.Contains(got, want) {
			t.Errorf("team %s: %q, want %q", team, got, want)
		}
	}
}
