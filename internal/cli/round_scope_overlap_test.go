package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/roundlease"
	"github.com/l4ci/rota/internal/tracker"
)

// scopeFx is an issues-backed repo where #1 is held by slot ben and #2 clashes
// with it on a declared scope only (`## Touches` names the same route, no file).
type scopeFx struct {
	root string
	deps *Deps
	git  []string // git calls, in order
}

func newScopeFx(t *testing.T, overlap string, extraCfg string) *scopeFx {
	t.Helper()
	root := gitRepo(t)
	write(t, filepath.Join(root, ".rota", "config.json"),
		`{"backlog":{"backend":"issues"},"issues":{"provider":"github"},"round":{"scope":"open","scopeOverlap":"`+overlap+`"`+extraCfg+`}}`)
	// dana is idle on her parked worktree; ben holds #1.
	wt := filepath.Join(t.TempDir(), "dana")
	if out, err := exec.Command("git", "-C", root, "worktree", "add", "-q", "-b", "park/dana", wt).CombinedOutput(); err != nil {
		t.Fatalf("worktree: %v %s", err, out)
	}
	write(t, filepath.Join(root, ".rota", "workers.json"),
		`{"architectureReview":{"at":"2020-01-01T00:00:00Z"},"slots":[{"name":"ben","task":"1","branch":"ben/1-holder"},{"name":"dana","branch":"park/dana","base":"feat/x","worktree":"`+wt+`"}]}`)
	body := "## Acceptance\\n- [ ] x\\n\\n## Touches\\n- POST /items\\n"
	issue := func(n int, title string) string {
		return fmt.Sprintf(`{"number":%d,"title":%q,"body":"%s","labels":[],"state":"OPEN"}`, n, title, body)
	}
	issues := map[string]string{"1": issue(1, "Holder"), "2": issue(2, "Candidate")}
	comments := map[int][]string{}
	fx := &scopeFx{root: root}
	deps := testDeps()
	deps.TrackerOptions = []tracker.Option{tracker.WithExec(func(_ context.Context, _, name string, args []string, _ []byte) ([]byte, []byte, int, error) {
		joined := strings.Join(args, " ")
		switch {
		case len(args) > 2 && args[0] == "api" && args[1] == "-X" && args[2] == "POST":
			// A posted comment is visible to the next read, as on a real forge.
			var n int
			fmt.Sscanf(args[3], "repos/{owner}/{repo}/issues/%d/comments", &n)
			id := len(comments[n]) + 100
			cm, _ := json.Marshal(map[string]any{"id": id, "body": strings.TrimPrefix(args[5], "body="), "created_at": "2026-01-01T00:00:00Z"})
			comments[n] = append(comments[n], string(cm))
			return []byte(fmt.Sprintf(`{"id":%d}`, id)), nil, 0, nil
		case len(args) > 1 && args[0] == "api" && strings.HasSuffix(args[1], "/comments") || len(args) > 2 && args[0] == "api" && strings.HasSuffix(args[len(args)-2], "/comments"):
			var n int
			for _, a := range args {
				fmt.Sscanf(a, "repos/{owner}/{repo}/issues/%d/comments", &n)
			}
			return []byte("[" + strings.Join(comments[n], ",") + "]"), nil, 0, nil
		case len(args) > 1 && args[0] == "issue" && args[1] == "list" && strings.Contains(joined, "--state closed"):
			return []byte(`[{"number":9,"title":"done","state":"CLOSED","labels":[],"closedAt":"2999-01-01T00:00:00Z"}]`), nil, 0, nil
		case len(args) > 1 && args[0] == "issue" && args[1] == "list":
			return []byte("[" + issues["1"] + "," + issues["2"] + "]"), nil, 0, nil
		case len(args) > 2 && args[0] == "issue" && args[1] == "view":
			return []byte(issues[args[2]]), nil, 0, nil
		}
		return []byte("[]"), nil, 0, nil
	}, func(n string) (string, error) { return "/fake/" + n, nil })}
	// The test process is the orchestrator: a fake process table holds the lease.
	le := roundlease.DefaultEnv()
	le.Alive = func(p int) bool { return p == scopePID }
	le.StartTime = func(p int) (uint64, bool) { return 99, p == scopePID }
	cd, err := rotastate.CommonDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := le.Acquire(cd, root, roundlease.Holder{PID: scopePID, Start: 99}, 1); err != nil {
		t.Fatal(err)
	}
	realGit := deps.Git
	deps.Git = func(ctx context.Context, dir string, args ...string) (git.Result, error) {
		fx.git = append(fx.git, strings.Join(args, " "))
		return realGit(ctx, dir, args...)
	}
	h := &cliHost{inSession: true}
	deps.Host = func(string) host.Host { return h }
	useHost(deps, h)
	deps.LeaseEnv = func() roundlease.Env { return le }
	fx.deps = deps
	return fx
}

const scopePID = 4242

// ready2 is whether `round candidates` reports #2 as ready.
func (f *scopeFx) ready2(t *testing.T) bool {
	t.Helper()
	code, out, errOut := rotaInWith(t, f.deps, f.root, "--json", "round", "candidates")
	if code != 0 {
		t.Fatalf("candidates exit %d: %s %s", code, out, errOut)
	}
	cs, _ := data(t, out)["candidates"].([]any)
	for _, c := range cs {
		m := c.(map[string]any)
		if m["id"] == "2" {
			return m["ready"] == true
		}
	}
	t.Fatalf("#2 is not a candidate: %s", out)
	return false
}

func TestScopeOverlapReachesRoundCandidates(t *testing.T) {
	if !newScopeFx(t, "warn", "").ready2(t) {
		t.Error("warn: a scope-only clash leaves #2 ready")
	}
	if newScopeFx(t, "block", "").ready2(t) {
		t.Error("block: a scope-only clash must make #2 not ready")
	}
}

// The architecture counter reads the candidates it is given: with an idle slot,
// a closed item since the last review and no ready candidate, a review is due.
func TestScopeOverlapReachesRoundArchitecture(t *testing.T) {
	trigger := func(overlap string) any {
		f := newScopeFx(t, overlap, "")
		code, out, errOut := rotaInWith(t, f.deps, f.root, "--json", "round", "architecture", "--check")
		if code != 0 {
			t.Fatalf("architecture exit %d: %s %s", code, out, errOut)
		}
		return data(t, out)["trigger"]
	}
	if got := trigger("warn"); got != nil {
		t.Errorf("warn: #2 is ready, so no queue-empty review: trigger %v", got)
	}
	if got := trigger("block"); got != "queue-empty" {
		t.Errorf("block: #2 is not ready, so the idle slot gets a review: trigger %v", got)
	}
}

// The status view's snapshot loads its candidates with the round's overlap mode.
func TestScopeOverlapReachesStatusViewSnapshot(t *testing.T) {
	ready := func(overlap string) bool {
		f := newScopeFx(t, overlap, "")
		c := &Ctx{Deps: f.deps, ctx: context.Background()}
		s, err := loadRoundSnap(c, f.root)
		if err != nil || s.CandsErr != "" {
			t.Fatalf("snapshot: %v %q", err, s.CandsErr)
		}
		for _, cd := range s.Cands {
			if cd.ID == "2" {
				return cd.Ready
			}
		}
		t.Fatalf("#2 missing from %+v", s.Cands)
		return false
	}
	if !ready("warn") {
		t.Error("warn: #2 stays ready")
	}
	if ready("block") {
		t.Error("block: #2 must not be ready")
	}
}

// Under warn a scope-only clash does not stop `round assign`, but it must be
// said on stderr, naming the holder and the shared scope.
func TestRoundAssignWarnsEachScopeOverlap(t *testing.T) {
	f := newScopeFx(t, "warn", "")
	code, out, errOut := rotaInWith(t, f.deps, f.root, "--json", "round", "assign", "2", "--agent", "dana", "--check-only", "--holder-pid", "4242")
	if code != 0 {
		t.Fatalf("assign --check-only exit %d: %s %s", code, out, errOut)
	}
	for _, want := range []string{"overlaps #1", "scopes post /items"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr lacks %q:\n%s", want, errOut)
		}
	}
}

// An autopilot assign under warn goes ahead despite a scope clash, and says
// which item it overlaps; under block the tick leaves #2 unassigned.
func TestRoundTickWarnsEachScopeOverlap(t *testing.T) {
	f := newScopeFx(t, "warn", `,"autopilot":true`)
	code, out, errOut := rotaInWith(t, f.deps, f.root, "--json", "round", "tick", "--holder-pid", "4242")
	if code != 0 || !strings.Contains(out, `"action": "assign", "target": "2"`) {
		t.Fatalf("tick should assign #2 under warn: %d %s %s", code, out, errOut)
	}
	for _, want := range []string{"#2 overlaps #1", "scopes post /items"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr lacks %q:\n%s", want, errOut)
		}
	}
}

// Under block the tick's candidate list already shows #2 as not ready, so no
// assign is attempted at all. Assign refuses the same clash on its own, so the
// outcome alone cannot tell the two apart; an attempt shows as the lease
// check Assign opens with (`git rev-parse --git-common-dir` through Deps.Git).
// Forge calls and workers.json are identical with and without the tick's
// ScopeOverlap wiring (Assign refuses block and the tick swallows it), so the
// lease check is the one signal that separates them; dropping the wiring at
// round_tick.go fails this test.
func TestRoundTickBlocksScopeOverlap(t *testing.T) {
	attempts := func(overlap string) int {
		f := newScopeFx(t, overlap, `,"autopilot":true`)
		code, out, errOut := rotaInWith(t, f.deps, f.root, "--json", "round", "tick", "--holder-pid", "4242")
		if code != 0 {
			t.Fatalf("tick exit %d: %s %s", code, out, errOut)
		}
		n := 0
		for _, g := range f.git {
			if g == "rev-parse --git-common-dir" {
				n++
			}
		}
		return n
	}
	warn, block := attempts("warn"), attempts("block")
	if block >= warn {
		t.Errorf("block: #2 is not ready, so the tick must make fewer assign attempts than under warn (block %d, warn %d)", block, warn)
	}
}

// `round start` lists the candidates it leaves behind with the round's overlap mode.
func TestScopeOverlapReachesRoundStart(t *testing.T) {
	ready := func(overlap string) bool {
		f := newScopeFx(t, overlap, "")
		code, out, errOut := rotaInWith(t, f.deps, f.root, "--json", "round", "start", "--holder-pid", "4242", "--slots", "2", "--base", "feat/x")
		if code != 0 {
			t.Fatalf("start exit %d: %s %s", code, out, errOut)
		}
		cs, _ := data(t, out)["candidates"].([]any)
		for _, c := range cs {
			if m := c.(map[string]any); m["id"] == "2" {
				return m["ready"] == true
			}
		}
		t.Fatalf("#2 is not a candidate: %s", out)
		return false
	}
	if !ready("warn") {
		t.Error("warn: #2 stays ready")
	}
	if ready("block") {
		t.Error("block: #2 must not be ready")
	}
}
