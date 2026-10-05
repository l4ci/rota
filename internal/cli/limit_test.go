package cli

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/hook"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/keepalive"
	"github.com/l4ci/rota/internal/limits"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/roundlease"
)

// limFake is a host that records what the watcher types and reads nothing:
// it stands in for tmux and herdr so no test reaches a real pane.
type limFake struct {
	host.Host
	mu    sync.Mutex
	sent  []string
	panes map[string]string
}

func (f *limFake) Name() string   { return "tmux" }
func (f *limFake) Require() error { return nil }
func (f *limFake) PaneOf(_ context.Context, slot, handle string) string {
	return handle
}
func (f *limFake) CapturePane(_ context.Context, pane string, _ int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.panes[pane]
}
func (f *limFake) SendPane(_ context.Context, pane, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, pane+": "+text)
	return nil
}

func limDeps() (*Deps, *limFake) {
	f := &limFake{panes: map[string]string{}}
	d := testDeps()
	d.Host = func(string) host.Host { return f }
	return d, f
}

func limProject(t *testing.T) string {
	t.Helper()
	dir := gitRepo(t)
	os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(`{"git":{"baseBranch":"feat/x"}}`), 0o644)
	return dir
}

func addLimit(t *testing.T, root string, e limits.Entry) limits.Entry {
	t.Helper()
	got, err := limits.Append(root, e)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func waitingEntry(resets time.Time) limits.Entry {
	return limits.Entry{Session: limits.Orchestrator, Window: limits.WindowFiveHour, Source: limits.SourceData,
		DetectedAt: limits.Time(resets.Add(-time.Hour)), ResetsAt: limits.Time(resets), Action: limits.ActionSleep, Status: limits.StatusWaiting}
}

func TestLimitStatusReadsTheLogBack(t *testing.T) {
	dir := limProject(t)
	code, out, _ := rotaIn(t, dir, "limit", "status", "--json")
	d := data(t, out)
	if code != 0 || d["watching"] != false || len(d["limits"].([]any)) != 0 {
		t.Fatalf("empty: %d %v", code, d)
	}
	e := addLimit(t, dir, waitingEntry(time.Date(2026, 10, 3, 15, 0, 0, 0, time.UTC)))
	code, out, _ = rotaIn(t, dir, "limit", "status", "--json")
	d = data(t, out)
	rows := d["limits"].([]any)
	if code != 0 || len(rows) != 1 {
		t.Fatalf("one: %d %v", code, d)
	}
	row := rows[0].(map[string]any)
	if row["id"] != e.ID || row["status"] != "waiting" || row["resetsAt"] != "2026-10-03T15:00:00Z" || row["cycles"] != float64(0) {
		t.Errorf("row %v", row)
	}
	if _, text, _ := rotaIn(t, dir, "limit", "status"); !strings.Contains(text, "l1\twaiting\torchestrator\tfive_hour") {
		t.Errorf("text %q", text)
	}
	cd, _ := rotastate.CommonDir(dir)
	limits.WriteWatching(cd, limits.Watching{PID: os.Getpid(), StartedAt: "x", Mode: limits.ModeWatch})
	if _, out, _ = rotaIn(t, dir, "limit", "status", "--json"); data(t, out)["watching"] != true {
		t.Errorf("a live watcher record must read as watching: %s", out)
	}
	limits.WriteWatching(cd, limits.Watching{PID: 1 << 30, StartedAt: "x", Mode: limits.ModeWatch})
	if _, out, _ = rotaIn(t, dir, "limit", "status", "--json"); data(t, out)["watching"] != false {
		t.Errorf("a dead watcher record must not: %s", out)
	}
	if code, _, _ := rotaIn(t, t.TempDir(), "limit", "status"); code != 3 {
		t.Errorf("outside a project: %d", code)
	}
}

func TestLimitWatchRefusals(t *testing.T) {
	dir := limProject(t)
	deps := testDeps()
	for _, argv := range [][]string{{"limit", "watch", "x"}, {"limit", "watch", "--timeout", "-1"}, {"limit", "watch", "--settle", "0"}} {
		if code, _, _ := rotaInWith(t, deps, dir, argv...); code != 2 {
			t.Errorf("%v: exit %d, want 2", argv, code)
		}
	}
	if code, _, _ := rotaInWith(t, deps, t.TempDir(), "limit", "watch"); code != 3 {
		t.Errorf("outside a project: exit %d, want 3", code)
	}
	// no lease: this process holds nothing
	code, out, _ := rotaInWith(t, deps, dir, "limit", "watch", "--json")
	if d := data(t, out); code != 4 || d["blockedBy"] != "no round" || d["changed"] != false {
		t.Fatalf("no lease: %d %v", code, d)
	}
	// a lease held by someone else
	cd, _ := rotastate.CommonDir(dir)
	env := roundlease.DefaultEnv()
	if _, _, _, err := env.Acquire(cd, dir, roundlease.Holder{PID: 1, Start: mustStart(env, 1)}, 2); err != nil {
		t.Fatal(err)
	}
	if code, out, _ = rotaInWith(t, deps, dir, "limit", "watch", "--json"); code != 4 || data(t, out)["blockedBy"] != "no round" {
		t.Fatalf("foreign lease: %d %s", code, out)
	}
	// a live supervisor holds it
	os.Remove(roundlease.Path(cd))
	self := env.Discover(os.Getpid(), os.Getenv)
	if _, _, _, err := env.Acquire(cd, dir, self, 0); err != nil {
		t.Fatal(err)
	}
	keepalive.WriteState(keepalive.StatePath(cd), keepalive.State{PID: os.Getpid(), Status: keepalive.StatusRunning, StartedAt: "x", RunStartedAt: "x"})
	if code, out, _ = rotaInWith(t, deps, dir, "limit", "watch", "--json"); code != 4 || data(t, out)["blockedBy"] != "supervised" {
		t.Fatalf("supervised: %d %s", code, out)
	}
	// the supervisor is gone: the holder may watch, once
	keepalive.WriteState(keepalive.StatePath(cd), keepalive.State{PID: 1 << 30, Status: keepalive.StatusRunning, StartedAt: "x", RunStartedAt: "x"})
	limits.WriteWatching(cd, limits.Watching{PID: 1, StartedAt: "x", Mode: limits.ModeWatch})
	deps.HolderPID = func() int { return os.Getpid() }
	if code, out, _ = rotaInWith(t, deps, dir, "limit", "watch", "--json"); code != 4 || data(t, out)["blockedBy"] != "watching" {
		t.Fatalf("watching: %d %s", code, out)
	}
	// a bad key is exit 70
	os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(`{"limits":{"mode":"wait"}}`), 0o644)
	if code, _, _ = rotaInWith(t, deps, dir, "limit", "watch"); code != 70 {
		t.Errorf("bad limits.mode: exit %d, want 70", code)
	}
}

func TestLimitWatchResumesAnEntryWhoseResetPassed(t *testing.T) {
	dir := limProject(t)
	deps, f := limDeps()
	t.Setenv("TMUX_PANE", "%9")
	t.Setenv("ROTA_TEST_HOLDER_PID", strconv.Itoa(os.Getpid()))
	t.Setenv("ROTA_TEST_NOW", "2026-10-03T16:00:00Z")
	cd, _ := rotastate.CommonDir(dir)
	env := roundlease.DefaultEnv()
	if _, _, _, err := env.Acquire(cd, dir, env.Discover(os.Getpid(), os.Getenv), 1); err != nil {
		t.Fatal(err)
	}
	addLimit(t, dir, waitingEntry(time.Date(2026, 10, 3, 15, 0, 0, 0, time.UTC)))

	code, out, errOut := rotaInWith(t, deps, dir, "limit", "watch", "--timeout", "1", "--settle", "0.05", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, out, errOut)
	}
	d := data(t, out)
	if d["resumed"] != float64(1) || d["waiting"] != float64(0) || d["failed"] != float64(0) || d["changed"] != true {
		t.Fatalf("data %v", d)
	}
	if len(f.sent) != 1 || f.sent[0] != "%9: The usage limit has reset. Continue where you left off." {
		t.Errorf("sent %v", f.sent)
	}
	if got := limits.Load(dir); len(got) != 1 || got[0].Status != limits.StatusResumed || got[0].Cycles != 1 {
		t.Errorf("log %+v", got)
	}
	if _, found := limits.ReadWatching(cd); found {
		t.Error("the watcher record must be removed on exit")
	}
}

func TestLimitWatchTextOnTheOrchestratorPane(t *testing.T) {
	dir := limProject(t)
	deps, f := limDeps()
	f.panes["%9"] = "work\nClaude usage limit reached. Your limit will reset at 5pm.\n"
	t.Setenv("TMUX_PANE", "%9")
	t.Setenv("ROTA_TEST_HOLDER_PID", strconv.Itoa(os.Getpid()))
	t.Setenv("ROTA_TEST_NOW", "2026-10-03T12:00:00Z")
	t.Setenv("TZ", "UTC")
	cd, _ := rotastate.CommonDir(dir)
	env := roundlease.DefaultEnv()
	env.Acquire(cd, dir, env.Discover(os.Getpid(), os.Getenv), 1)

	code, out, _ := rotaInWith(t, deps, dir, "limit", "watch", "--timeout", "0.5", "--settle", "0.05", "--json")
	d := data(t, out)
	if code != 0 || d["waiting"] != float64(1) {
		t.Fatalf("exit %d: %v", code, d)
	}
	e := d["limits"].([]any)[0].(map[string]any)
	if e["source"] != "text" || e["session"] != "orchestrator" || e["action"] != "sleep" || e["window"] != "unknown" {
		t.Errorf("entry %v", e)
	}
	if _, err := time.Parse(time.RFC3339, e["resetsAt"].(string)); err != nil {
		t.Errorf("resetsAt %v", e["resetsAt"])
	}
	if len(f.sent) != 0 {
		t.Errorf("nothing is due yet: %v", f.sent)
	}
}

func TestOrchestratorDataIsTheNewestSessionFileOfTheRoot(t *testing.T) {
	dir := limProject(t)
	cd, _ := rotastate.CommonDir(dir)
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	write := func(id, cwd string, at time.Time, used int, reset time.Time) {
		payload := `{"session_id":"` + id + `","cwd":"` + cwd + `","rate_limits":{"five_hour":{"used_percentage":` + strconv.Itoa(used) + `,"resets_at":` + strconv.FormatInt(reset.Unix(), 10) + `}}}`
		if err := hook.Dump(cd, []byte(payload), at); err != nil {
			t.Fatal(err)
		}
	}
	write("old", dir, now.Add(-time.Hour), 100, now.Add(time.Hour))
	write("slot", filepath.Join(dir, ".worktrees", "ben"), now.Add(-time.Second), 100, now.Add(5*time.Hour))
	read := orchestratorData(cd, dir, 120*time.Second)
	d := read(now)
	if !d.Limited || !d.ResetsAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("a limit older than the max age still counts: %+v", d)
	}
	write("new", dir, now.Add(-10*time.Second), 40, now.Add(time.Hour))
	if d := read(now); d.Limited || !d.Known {
		t.Fatalf("the newest file of the root wins: %+v", d)
	}
	if d := read(now.Add(10 * time.Minute)); d.Known {
		t.Fatalf("a stale file that shows no limit is no data: %+v", d)
	}
}
