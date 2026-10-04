package keepalive

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/hook"
	"github.com/l4ci/rota/internal/roundlease"
)

var t0 = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)

// rig is a fake world: a clock that moves one second per read, a handoff
// file, a process table with the supervisor alive, and scripted children.
type rig struct {
	t        *testing.T
	dir      string
	now      time.Time
	mu       sync.Mutex
	handoff  HandoffRead
	starts   [][]string
	envs     [][]string
	script   func(n int, r *rig) Exit // runs when the n-th child (1-based) "runs"
	blockOn  chan struct{}            // when set, the child waits for a signal
	signaled []os.Signal
	sleeps   []time.Duration
	escalate []string
	notified []string
	spawnErr error
}

func newRig(t *testing.T) *rig { return &rig{t: t, dir: t.TempDir(), now: t0} }

func (r *rig) clock() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.now = r.now.Add(time.Second)
	return r.now
}

// write puts a handoff with the given content, stamped now.
func (r *rig) write(sha string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.now = r.now.Add(time.Second)
	r.handoff = HandoffRead{Handoff: hook.Handoff{Exists: true, ModTime: r.now}, SHA: sha}
}

func (r *rig) clear() { r.handoff = HandoffRead{} }

type fakeChild struct {
	r    *rig
	exit Exit
}

func (c *fakeChild) Signal(s os.Signal) error {
	c.r.mu.Lock()
	c.r.signaled = append(c.r.signaled, s)
	ch := c.r.blockOn
	c.r.mu.Unlock()
	if ch != nil {
		close(ch)
	}
	return nil
}

func (c *fakeChild) Wait() (Exit, error) {
	c.r.mu.Lock()
	ch := c.r.blockOn
	c.r.mu.Unlock()
	if ch != nil {
		<-ch
		return Exit{Code: 130, Signal: "SIGINT"}, nil
	}
	return c.exit, nil
}

func (r *rig) env(signals <-chan os.Signal) Env {
	procs := map[int]uint64{10: 100}
	le := roundlease.Env{Host: "h", Now: func() time.Time { return t0 },
		Alive:     func(p int) bool { _, ok := procs[p]; return ok },
		StartTime: func(p int) (uint64, bool) { s, ok := procs[p]; return s, ok }}
	return Env{
		Now:     r.clock,
		Signals: signals,
		Lease:   le,
		Holder:  roundlease.Holder{PID: 10, Start: 100},
		Sleep:   func(d time.Duration, intr <-chan struct{}) bool { r.sleeps = append(r.sleeps, d); return true },
		Handoff: func() HandoffRead { r.mu.Lock(); defer r.mu.Unlock(); return r.handoff },
		Spawn: func(argv, extra []string) (Child, error) {
			if r.spawnErr != nil {
				return nil, r.spawnErr
			}
			r.starts = append(r.starts, argv)
			r.envs = append(r.envs, extra)
			ex := Exit{}
			if r.script != nil {
				ex = r.script(len(r.starts), r)
			}
			return &fakeChild{r: r, exit: ex}, nil
		},
		Escalate: func(issue int, title, body string) (string, []string, error) {
			r.escalate = append(r.escalate, title+"\n"+body)
			return "e1", nil, nil
		},
		Notify: func(title, body string) { r.notified = append(r.notified, title) },
	}
}

func (r *rig) opts() Options {
	return Options{Command: []string{"claude", "--x"}, Root: r.dir, CommonDir: r.dir,
		HandoffPath: filepath.Join(r.dir, ".rota", "handoff", "main.md"), HandoffMaxAge: 900 * time.Second,
		MaxRestarts: 10, Breaker: 3, Backoff: 5 * time.Second, Prompt: "go on", EscalateIssue: 7}
}

func (r *rig) state() State {
	r.t.Helper()
	st, found, err := ReadState(StatePath(r.dir))
	if err != nil || !found {
		r.t.Fatalf("state: %v %v", found, err)
	}
	return st
}

func (r *rig) lease(env Env) roundlease.State {
	_, st, _ := env.Lease.Read(r.dir)
	return st
}

func TestRestartsOnFreshHandoffWithPromptLastOnRestartsOnly(t *testing.T) {
	r := newRig(t)
	r.script = func(n int, r *rig) Exit {
		if n == 1 {
			r.write("a")
		} else {
			r.clear() // the second run exits deliberately
		}
		return Exit{}
	}
	env := r.env(nil)
	res, err := Run(env, r.opts())
	if err != nil || res.StopReason != StopNoHandoff || res.Restarts != 1 || !res.Changed {
		t.Fatalf("%v %+v", err, res)
	}
	if len(r.starts) != 2 {
		t.Fatalf("starts: %v", r.starts)
	}
	if got := strings.Join(r.starts[0], " "); got != "claude --x" {
		t.Errorf("first start must carry no prompt: %q", got)
	}
	if got := r.starts[1]; got[len(got)-1] != "go on" || len(got) != 3 {
		t.Errorf("restart must append the prompt last: %v", got)
	}
	if len(r.sleeps) != 1 || r.sleeps[0] != 5*time.Second {
		t.Errorf("backoff: %v", r.sleeps)
	}
	if r.envs[0][0] != "ROTA_ROUND_HOLDER_PID=10" {
		t.Errorf("child env: %v", r.envs[0])
	}
	st := r.state()
	if st.Status != StatusStopped || st.StopReason != StopNoHandoff || st.Restarts != 1 || st.PID != 10 {
		t.Errorf("state: %+v", st)
	}
	if r.lease(env) != roundlease.None {
		t.Errorf("lease must be released on stop")
	}
	if len(r.escalate)+len(r.notified) != 0 {
		t.Errorf("no-handoff sends nothing")
	}
}

func TestNoHandoffOrStaleHandoffDoesNotRestart(t *testing.T) {
	for name, setup := range map[string]func(r *rig){
		"none": func(r *rig) {},
		"too old": func(r *rig) {
			r.handoff = HandoffRead{Handoff: hook.Handoff{Exists: true, ModTime: t0.Add(-time.Hour)}, SHA: "a"}
		},
		"consumed": func(r *rig) { r.clear() },
	} {
		r := newRig(t)
		setup(r)
		res, err := Run(r.env(nil), r.opts())
		if err != nil || res.StopReason != StopNoHandoff || len(r.starts) != 1 || res.Restarts != 0 {
			t.Errorf("%s: %v %+v starts=%d", name, err, res, len(r.starts))
		}
	}
}

func TestLeaseHeldByAnotherRefusesAndStartsNothing(t *testing.T) {
	r := newRig(t)
	env := r.env(nil)
	procs := map[int]uint64{10: 100, 20: 200}
	env.Lease.Alive = func(p int) bool { _, ok := procs[p]; return ok }
	env.Lease.StartTime = func(p int) (uint64, bool) { s, ok := procs[p]; return s, ok }
	if _, _, _, err := env.Lease.Acquire(r.dir, "/other", roundlease.Holder{PID: 20, Start: 200}, 3); err != nil {
		t.Fatal(err)
	}
	_, err := Run(env, r.opts())
	var held *roundlease.HeldError
	if !errors.As(err, &held) || held.Lease.PID != 20 || len(r.starts) != 0 {
		t.Fatalf("%v starts=%d", err, len(r.starts))
	}
	if _, found, _ := ReadState(StatePath(r.dir)); found {
		t.Errorf("a refused run must not write the state file")
	}
	if cur, _, _ := env.Lease.Read(r.dir); cur.PID != 20 {
		t.Errorf("the other holder's lease must stay: %+v", cur)
	}
}

func TestStaleLeaseIsReclaimedAndRoundNumberKept(t *testing.T) {
	r := newRig(t)
	env := r.env(nil)
	dead := roundlease.Holder{PID: 99, Start: 5}
	live := env.Lease
	live.Alive = func(int) bool { return true }
	live.StartTime = func(p int) (uint64, bool) { return 5, true }
	live.Acquire(r.dir, "/old", dead, 4)
	res, err := Run(env, r.opts())
	if err != nil || res.Reclaimed.PID != 99 || len(res.Warnings) == 0 {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestBreakerTripsAfterNoProgressRestartsAndEscalates(t *testing.T) {
	r := newRig(t)
	r.write("same") // left before the run; no child writes another
	env := r.env(nil)
	res, err := Run(env, r.opts())
	if err != nil || res.StopReason != StopBreaker || res.NoProgress != 3 || res.Restarts != 2 || len(r.starts) != 3 {
		t.Fatalf("%v %+v starts=%d", err, res, len(r.starts))
	}
	if res.Escalation != "e1" || len(r.escalate) != 1 {
		t.Fatalf("escalation: %q %v", res.Escalation, r.escalate)
	}
	e := r.escalate[0]
	for _, want := range []string{"Orchestrator keepalive stopped: breaker", "Restarts: 2", "no new handoff: 3", "main.md", "keepalive.json", "Run rota keepalive run again after fixing the cause; the handoff is kept."} {
		if !strings.Contains(e, want) {
			t.Errorf("escalation lacks %q:\n%s", want, e)
		}
	}
	if st := r.state(); st.Escalation != "e1" || st.StopReason != StopBreaker || st.NoProgress != 3 {
		t.Errorf("state: %+v", st)
	}
	if r.lease(env) != roundlease.None {
		t.Errorf("lease must be released")
	}
}

func TestBreakerWithoutEscalateIssueOnlyNotifiesAndWarns(t *testing.T) {
	r := newRig(t)
	r.write("same")
	o := r.opts()
	o.EscalateIssue = 0
	res, err := Run(r.env(nil), o)
	if err != nil || res.StopReason != StopBreaker || res.Escalation != "" || len(r.escalate) != 0 {
		t.Fatalf("%v %+v", err, res)
	}
	if len(r.notified) != 1 || !strings.Contains(r.notified[0], "breaker") {
		t.Errorf("notify: %v", r.notified)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "escalateIssue is unset") {
		t.Errorf("warnings: %v", res.Warnings)
	}
}

func TestEscalationFailureIsAWarningAndTheStopStands(t *testing.T) {
	r := newRig(t)
	r.write("same")
	env := r.env(nil)
	env.Escalate = func(int, string, string) (string, []string, error) {
		return "", nil, errors.New("escalation e1 is still pending on issue #7")
	}
	res, err := Run(env, r.opts())
	if err != nil || res.StopReason != StopBreaker || res.Escalation != "" || len(res.Warnings) != 1 {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestMaxRestartsStopsEvenWithProgress(t *testing.T) {
	r := newRig(t)
	r.script = func(n int, r *rig) Exit { r.write(string(rune('a' + n))); return Exit{} }
	o := r.opts()
	o.MaxRestarts = 2
	res, err := Run(r.env(nil), o)
	if err != nil || res.StopReason != StopMaxRestarts || res.Restarts != 2 || len(r.starts) != 3 || res.NoProgress != 0 {
		t.Fatalf("%v %+v starts=%d", err, res, len(r.starts))
	}
	if len(r.escalate) != 1 || !strings.Contains(r.escalate[0], "max-restarts") {
		t.Errorf("escalate: %v", r.escalate)
	}
}

func TestZeroMaxRestartsStopsAtTheFirstHandoffExit(t *testing.T) {
	r := newRig(t)
	r.script = func(n int, r *rig) Exit { r.write("a"); return Exit{} }
	o := r.opts()
	o.MaxRestarts = 0
	res, _ := Run(r.env(nil), o)
	if res.StopReason != StopMaxRestarts || len(r.starts) != 1 {
		t.Fatalf("%+v", res)
	}
}

func TestProgressResetsTheBreaker(t *testing.T) {
	r := newRig(t)
	// Every run writes a different handoff, ten times, then a stuck one.
	r.script = func(n int, r *rig) Exit {
		switch {
		case n <= 8:
			r.write(string(rune('a' + n)))
		case n == 9:
			// stuck: leaves the previous handoff alone
		default:
			r.clear()
		}
		return Exit{}
	}
	o := r.opts()
	o.MaxRestarts = 50
	res, err := Run(r.env(nil), o)
	if err != nil || res.StopReason != StopNoHandoff || len(r.starts) != 10 {
		t.Fatalf("%v %+v starts=%d", err, res, len(r.starts))
	}
	if res.NoProgress != 1 {
		t.Errorf("one stuck run after eight with progress: %d", res.NoProgress)
	}
}

func TestSameContentRewrittenCountsAsNoProgress(t *testing.T) {
	r := newRig(t)
	r.write("same")
	r.script = func(n int, r *rig) Exit { r.write("same"); return Exit{} } // newer mtime, same sha
	res, _ := Run(r.env(nil), r.opts())
	if res.StopReason != StopBreaker || len(r.starts) != 3 {
		t.Fatalf("%+v", res)
	}
}

func TestSignalIsForwardedAndStopsWithoutRestart(t *testing.T) {
	r := newRig(t)
	r.blockOn = make(chan struct{})
	r.write("a") // a fresh handoff must not matter once interrupted
	sig := make(chan os.Signal, 1)
	env := r.env(sig)
	started := make(chan struct{})
	spawn := env.Spawn
	env.Spawn = func(argv, e []string) (Child, error) {
		c, err := spawn(argv, e)
		close(started)
		return c, err
	}
	go func() { <-started; sig <- syscall.SIGINT }()
	res, err := Run(env, r.opts())
	if err != nil || res.StopReason != StopInterrupted || len(r.starts) != 1 || res.Restarts != 0 {
		t.Fatalf("%v %+v", err, res)
	}
	if len(r.signaled) != 1 || r.signaled[0] != syscall.SIGINT {
		t.Errorf("forwarded: %v", r.signaled)
	}
	if res.LastExit.Signal != "SIGINT" || len(r.escalate)+len(r.notified) != 0 {
		t.Errorf("%+v %v", res, r.escalate)
	}
	if r.lease(env) != roundlease.None {
		t.Errorf("lease must be released")
	}
}

func TestSignalDuringBackoffStopsWithoutRestart(t *testing.T) {
	r := newRig(t)
	r.script = func(n int, r *rig) Exit { r.write("a"); return Exit{} }
	env := r.env(nil)
	env.Sleep = func(d time.Duration, intr <-chan struct{}) bool { return false }
	res, _ := Run(env, r.opts())
	if res.StopReason != StopInterrupted || len(r.starts) != 1 {
		t.Fatalf("%+v", res)
	}
}

func TestSpawnFailureReleasesTheLeaseAndReturnsSpawnError(t *testing.T) {
	r := newRig(t)
	r.spawnErr = errors.New("exec: not found")
	env := r.env(nil)
	res, err := Run(env, r.opts())
	var se *SpawnError
	if !errors.As(err, &se) || res.Changed {
		t.Fatalf("%v %+v", err, res)
	}
	if r.lease(env) != roundlease.None || r.state().StopReason != StopSpawnFailed {
		t.Errorf("lease released and state stopped expected: %+v", r.state())
	}
}

func TestLeaseNoLongerOursIsLeftAlone(t *testing.T) {
	r := newRig(t)
	env := r.env(nil)
	procs := map[int]uint64{10: 100, 20: 200}
	env.Lease.Alive = func(p int) bool { _, ok := procs[p]; return ok }
	env.Lease.StartTime = func(p int) (uint64, bool) { s, ok := procs[p]; return s, ok }
	r.script = func(n int, r *rig) Exit {
		// Someone cleared our lease and took it while the child ran.
		os.Remove(roundlease.Path(r.dir))
		env.Lease.Acquire(r.dir, "/o", roundlease.Holder{PID: 20, Start: 200}, 9)
		return Exit{}
	}
	if _, err := Run(env, r.opts()); err != nil {
		t.Fatal(err)
	}
	if cur, st, _ := env.Lease.Read(r.dir); st != roundlease.Live || cur.PID != 20 {
		t.Fatalf("the new holder's lease must survive: %+v %v", cur, st)
	}
}

func TestLoadSettingsDefaultsAndRanges(t *testing.T) {
	load := func(body string) (Settings, error) {
		p := filepath.Join(t.TempDir(), "config.json")
		if body != "" {
			os.WriteFile(p, []byte(body), 0o644)
		}
		return LoadSettings(config.Load(p))
	}
	s, err := load("")
	if err != nil || s.MaxRestarts != 10 || s.Breaker != 3 || s.Backoff != 5*time.Second || s.Prompt != DefaultPrompt || s.EscalateIssue != 0 || s.HandoffMaxAge != 900*time.Second {
		t.Fatalf("defaults: %v %+v", err, s)
	}
	if s, err := load(`{"orchestrator":{"keepaliveMaxRestarts":0,"keepaliveBackoffSeconds":0,"escalateIssue":12,"restartPrompt":"go"}}`); err != nil || s.MaxRestarts != 0 || s.Backoff != 0 || s.EscalateIssue != 12 || s.Prompt != "go" {
		t.Fatalf("explicit: %v %+v", err, s)
	}
	for _, bad := range []string{
		`{"orchestrator":{"keepaliveBreaker":0}}`,
		`{"orchestrator":{"keepaliveMaxRestarts":-1}}`,
		`{"orchestrator":{"keepaliveBackoffSeconds":"x"}}`,
		`{"orchestrator":{"escalateIssue":-3}}`,
		`{"orchestrator":{"restartPrompt":""}}`,
		`{"orchestrator":{"restartPrompt":4}}`,
	} {
		if _, err := load(bad); err == nil {
			t.Errorf("%s must be rejected", bad)
		}
	}
}

func TestLimitsLoopRunsBesideTheChildAndEndsBeforeTheLeaseIsReleased(t *testing.T) {
	r := newRig(t)
	env := r.env(nil)
	started := make(chan struct{})
	var leaseAtStop roundlease.State
	var heldWhileRunning roundlease.State
	env.Limits = func(ctx context.Context) []string {
		close(started)
		_, heldWhileRunning, _ = env.Lease.Read(r.dir)
		<-ctx.Done()
		_, leaseAtStop, _ = env.Lease.Read(r.dir)
		return []string{"limits warning"}
	}
	r.script = func(n int, r *rig) Exit {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Error("the limits loop never started")
		}
		return Exit{}
	}
	res, err := Run(env, r.opts())
	if err != nil || res.StopReason != StopNoHandoff {
		t.Fatalf("%v %+v", err, res)
	}
	if heldWhileRunning != roundlease.Live || leaseAtStop != roundlease.Live {
		t.Errorf("the lease must be held while the loop runs and when it is told to stop: %v %v", heldWhileRunning, leaseAtStop)
	}
	if r.lease(env) != roundlease.None {
		t.Error("the lease must be released after the loop ended")
	}
	if len(res.Warnings) != 1 || res.Warnings[0] != "limits warning" {
		t.Errorf("the loop's warnings: %v", res.Warnings)
	}
}
