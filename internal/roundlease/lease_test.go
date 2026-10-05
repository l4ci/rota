package roundlease

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/rotastate"
)

type procs map[int]uint64 // pid -> start; absent means dead

func fakeEnv(host string, p procs) Env {
	return Env{
		Host:      host,
		Alive:     func(pid int) bool { _, ok := p[pid]; return ok },
		StartTime: func(pid int) (uint64, bool) { s, ok := p[pid]; return s, ok },
		Now:       func() time.Time { return time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC) },
	}
}

func TestAcquireTakesThenRefusesSecondHolder(t *testing.T) {
	dir := t.TempDir()
	e := fakeEnv("h1", procs{10: 100, 20: 200})
	a := Holder{PID: 10, Start: 100, Pane: "p1", PaneHost: "herdr"}
	b := Holder{PID: 20, Start: 200, Pane: "p2", PaneHost: "herdr"}

	l, out, _, err := e.Acquire(dir, "/r", a, 4)
	if err != nil || out != Taken || l.Round != 4 {
		t.Fatalf("first acquire: %v %v %+v", err, out, l)
	}
	_, _, _, err = e.Acquire(dir, "/r2", b, 5)
	var held *HeldError
	if !errors.As(err, &held) || held.Lease.PID != 10 || held.State != Live {
		t.Fatalf("second holder must be refused naming the first: %v", err)
	}
	cur, st, _ := e.Read(dir)
	if st != Live || cur.PID != 10 || cur.Root != "/r" {
		t.Fatalf("refusal must not write: %+v %v", cur, st)
	}
}

func TestAcquireRenewsSameHolderKeepingRound(t *testing.T) {
	dir := t.TempDir()
	e := fakeEnv("h1", procs{10: 100})
	h := Holder{PID: 10, Start: 100}
	e.Acquire(dir, "/r", h, 4)
	l, out, _, err := e.Acquire(dir, "/r", h, 9)
	if err != nil || out != Renewed || l.Round != 4 {
		t.Fatalf("renew: %v %v %+v", err, out, l)
	}
	// Same pane, new pid (the orchestrator's shell changed): still the holder.
	e2 := fakeEnv("h1", procs{10: 100, 11: 110})
	e2.Acquire(dir, "/r", Holder{PID: 10, Start: 100, Pane: "p", PaneHost: "tmux"}, 4)
	_, out, _, err = e2.Acquire(dir, "/r", Holder{PID: 11, Start: 110, Pane: "p", PaneHost: "tmux"}, 5)
	if err != nil || out != Renewed {
		t.Fatalf("same pane must renew: %v %v", err, out)
	}
}

func TestStaleLeaseIsReclaimed(t *testing.T) {
	dir := t.TempDir()
	e := fakeEnv("h1", procs{10: 100})
	e.Acquire(dir, "/r", Holder{PID: 10, Start: 100}, 4)

	dead := fakeEnv("h1", procs{20: 200}) // pid 10 gone
	if _, st, _ := dead.Read(dir); st != Stale {
		t.Fatalf("dead pid must be stale, got %v", st)
	}
	l, out, prev, err := dead.Acquire(dir, "/r", Holder{PID: 20, Start: 200}, 5)
	if err != nil || out != Reclaimed || prev.PID != 10 || l.PID != 20 || l.Round != 5 {
		t.Fatalf("reclaim: %v %v prev=%+v l=%+v", err, out, prev, l)
	}
}

func TestPIDReuseIsStale(t *testing.T) {
	dir := t.TempDir()
	e := fakeEnv("h1", procs{10: 100})
	e.Acquire(dir, "/r", Holder{PID: 10, Start: 100}, 1)
	reused := fakeEnv("h1", procs{10: 999}) // same pid, different process
	if _, st, _ := reused.Read(dir); st != Stale {
		t.Fatalf("reused pid must be stale, got %v", st)
	}
}

func TestForeignHostIsHeld(t *testing.T) {
	dir := t.TempDir()
	fakeEnv("other", procs{10: 100}).Acquire(dir, "/r", Holder{PID: 10, Start: 100}, 1)
	e := fakeEnv("h1", procs{})
	if _, st, _ := e.Read(dir); st != Foreign {
		t.Fatalf("other host must be foreign, got %v", st)
	}
	_, _, _, err := e.Acquire(dir, "/r", Holder{PID: 20}, 2)
	var held *HeldError
	if !errors.As(err, &held) || held.State != Foreign {
		t.Fatalf("foreign lease must refuse: %v", err)
	}
	if _, cleared, _ := e.ClearStale(dir); cleared {
		t.Fatal("ClearStale must not remove a foreign lease")
	}
}

func TestCorruptLeaseIsStale(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Dir(Path(dir)), 0o755)
	os.WriteFile(Path(dir), []byte("{not json"), 0o644)
	e := fakeEnv("h1", procs{20: 1})
	if _, st, _ := e.Read(dir); st != Stale {
		t.Fatalf("corrupt must be stale, got %v", st)
	}
	if _, out, _, err := e.Acquire(dir, "/r", Holder{PID: 20, Start: 1}, 1); err != nil || out != Reclaimed {
		t.Fatalf("corrupt must be reclaimable: %v %v", err, out)
	}
}

func TestReleaseAndClearStale(t *testing.T) {
	dir := t.TempDir()
	e := fakeEnv("h1", procs{10: 100, 20: 200})
	a, b := Holder{PID: 10, Start: 100}, Holder{PID: 20, Start: 200}
	e.Acquire(dir, "/r", a, 1)
	if ok, _ := e.Release(dir, b); ok {
		t.Fatal("a non-holder must not release")
	}
	if ok, err := e.Release(dir, a); !ok || err != nil {
		t.Fatalf("holder release: %v %v", ok, err)
	}
	if _, st, _ := e.Read(dir); st != None {
		t.Fatalf("released lease must be gone, got %v", st)
	}

	e.Acquire(dir, "/r", a, 1)
	if _, cleared, _ := e.ClearStale(dir); cleared {
		t.Fatal("ClearStale must keep a live lease")
	}
	gone := fakeEnv("h1", procs{})
	l, cleared, err := gone.ClearStale(dir)
	if err != nil || !cleared || l.PID != 10 {
		t.Fatalf("ClearStale of dead holder: %v %v %+v", err, cleared, l)
	}
}

// Every worktree of a repo shares one common dir, hence one lease.
func TestCommonDirIsSharedAcrossWorktrees(t *testing.T) {
	repo := t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(repo, "init", "-q", "-b", "main")
	run(repo, "commit", "-q", "--allow-empty", "-m", "x")
	wt := filepath.Join(t.TempDir(), "wt")
	run(repo, "worktree", "add", "-q", wt, "-b", "other")

	a, err := rotastate.CommonDir(repo)
	if err != nil {
		t.Fatal(err)
	}
	b, err := rotastate.CommonDir(wt)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("worktrees must share a common dir: %s vs %s", a, b)
	}
	e := fakeEnv("h1", procs{10: 1, 20: 2})
	if _, _, _, err := e.Acquire(a, repo, Holder{PID: 10, Start: 1}, 1); err != nil {
		t.Fatal(err)
	}
	var held *HeldError
	if _, _, _, err := e.Acquire(b, wt, Holder{PID: 20, Start: 2}, 2); !errors.As(err, &held) {
		t.Fatalf("a second worktree must be refused: %v", err)
	}
}

func TestDiscoverUsesOverrideAndPaneEnv(t *testing.T) {
	e := fakeEnv("h1", procs{77: 5})
	env := map[string]string{"TMUX_PANE": "%3"}
	h := e.Discover(77, func(k string) string { return env[k] })
	if h.PID != 77 || h.Start != 5 || h.Pane != "%3" || h.PaneHost != "tmux" {
		t.Fatalf("%+v", h)
	}
	env = map[string]string{"HERDR_PANE_ID": "p_9", "TMUX_PANE": "%3"}
	if h := e.Discover(77, func(k string) string { return env[k] }); h.PaneHost != "herdr" || h.Pane != "p_9" {
		t.Fatalf("herdr pane wins: %+v", h)
	}
}

func TestDiscoverWalksPastShells(t *testing.T) {
	// The test process's parent is `go test` or a shell; either way the
	// result is a live pid with a start time, never rota's own pid.
	e := DefaultEnv()
	h := e.Discover(0, func(string) string { return "" })
	if h.PID <= 0 || h.PID == os.Getpid() || !e.Alive(h.PID) {
		t.Fatalf("bad holder %+v", h)
	}
}

func TestDiscoverHonoursHolderPIDEnvButExplicitPIDWins(t *testing.T) {
	e := fakeEnv("h1", procs{55: 7, 77: 5})
	env := map[string]string{HolderPIDEnv: "55", "TMUX_PANE": "%3"}
	get := func(k string) string { return env[k] }
	if h := e.Discover(0, get); h.PID != 55 || h.Start != 7 || h.Pane != "%3" {
		t.Fatalf("env must name the holder when no pid is given: %+v", h)
	}
	if h := e.Discover(77, get); h.PID != 77 {
		t.Fatalf("an explicit --holder-pid must win over the env: %+v", h)
	}
	for _, bad := range []string{"", "x", "0", "-4"} {
		env[HolderPIDEnv] = bad
		if h := e.Discover(0, get); h.PID == 55 || h.PID <= 0 {
			t.Fatalf("env %q must fall through to the ancestor walk: %+v", bad, h)
		}
	}
}

func TestAcquireNumbersAnUnnumberedLeaseOfTheSameHolder(t *testing.T) {
	dir := t.TempDir()
	e := fakeEnv("h1", procs{10: 100, 20: 200})
	h := Holder{PID: 10, Start: 100}
	if l, out, _, err := e.Acquire(dir, "/r", h, 0); err != nil || out != Taken || l.Round != 0 {
		t.Fatalf("unnumbered take: %v %v %+v", err, out, l)
	}
	// Another holder is still refused.
	if _, _, _, err := e.Acquire(dir, "/r", Holder{PID: 20, Start: 200}, 5); err == nil {
		t.Fatal("a second holder must be refused")
	}
	// An unnumbered renewal stays unnumbered.
	if l, out, _, _ := e.Acquire(dir, "/r", h, 0); out != Renewed || l.Round != 0 {
		t.Fatalf("round 0 renewal: %v %+v", out, l)
	}
	l, out, _, err := e.Acquire(dir, "/r", h, 3)
	if err != nil || out != Numbered || l.Round != 3 {
		t.Fatalf("numbering: %v %v %+v", err, out, l)
	}
	if cur, _, _ := e.Read(dir); cur.Round != 3 || cur.StartedAt == "" {
		t.Fatalf("the number must be on disk: %+v", cur)
	}
	// Later renewals keep the number.
	if l, out, _, _ := e.Acquire(dir, "/r", h, 9); out != Renewed || l.Round != 3 {
		t.Fatalf("renewal must keep the number: %v %+v", out, l)
	}
}

// tree is a recorded process table: pid -> (ppid, comm).
type tree map[int]struct {
	pp   int
	comm string
}

func (t tree) env(self int) Env {
	e := fakeEnv("h1", procs{})
	e.Parent = func() int { return self }
	e.Proc = func(pid int) (int, string, bool) {
		p, ok := t[pid]
		return p.pp, p.comm, ok
	}
	return e
}

// TestDiscoverStopsAtTheHostServer pins #205: from a plain shell in a herdr or
// tmux pane the holder is that pane's shell, not the server every pane shares;
// with an agent in the pane, the agent.
func TestDiscoverStopsAtTheHostServer(t *testing.T) {
	none := func(string) string { return "" }
	for _, server := range []string{"herdr", "tmux: server"} {
		// server(4089) -> zsh(5000) -> rota's parent shell(5001) -> rota
		plain := tree{4089: {1, server}, 5000: {4089, "zsh"}, 5001: {5000, "bash"}}
		if h := plain.env(5001).Discover(0, none); h.PID != 5000 {
			t.Errorf("%s, plain shell: holder %d, want the pane's shell 5000", server, h.PID)
		}
		// A second pane on the same server is a different holder.
		other := tree{4089: {1, server}, 6000: {4089, "zsh"}}
		if h := other.env(6000).Discover(0, none); h.PID != 6000 {
			t.Errorf("%s, second pane: holder %d, want 6000", server, h.PID)
		}
		// server -> zsh -> claude -> bash -> rota: the agent holds.
		agent := tree{4089: {1, server}, 5000: {4089, "zsh"}, 5100: {5000, "claude"}, 5101: {5100, "bash"}}
		if h := agent.env(5101).Discover(0, none); h.PID != 5100 {
			t.Errorf("%s, agent in the pane: holder %d, want claude 5100", server, h.PID)
		}
	}
	// rota run straight from the server's child: that child is the holder.
	direct := tree{4089: {1, "herdr"}}
	if h := direct.env(4089).Discover(0, none); h.PID != 4089 {
		t.Errorf("nothing below the server: holder %d, want the parent 4089", h.PID)
	}
}
