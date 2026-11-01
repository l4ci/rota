package worker

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/pidlive"
	"github.com/l4ci/rota/internal/rotastate"
)

// A gate that queued behind the land lock reads the PR again: it merged while
// the gate waited, so the verdict is already-merged, not a refusal about a head
// that has landed.
func TestGateWaitedOnLockFindsPRMerged(t *testing.T) {
	w := newWorld(t, ghURL)
	env := w.env(false).WithDefaults()
	type out struct {
		res GateResult
		err error
	}
	done := make(chan out, 1)
	err := env.withLandLock(bg, w.dir, "", func(bool) error {
		go func() {
			res, err := w.gate(false, GateOpts{})
			done <- out{res, err}
		}()
		time.Sleep(300 * time.Millisecond) // the gate is queued on the lock
		w.forge("state", "MERGED")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case o := <-done:
		if o.err != nil || o.res.Verdict != GateAlreadyMerged || o.res.Changed || !o.res.AlreadyMerged {
			t.Fatalf("verdict %q changed=%v err %v: %s", o.res.Verdict, o.res.Changed, o.err, o.res.Err)
		}
		if strings.Contains(o.res.Err, "origin") || o.res.Verdict == GateCheckBroke {
			t.Errorf("must not report a missing origin: %q", o.res.Err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("gate did not finish after the lock was released")
	}
}

// A gate that starts while another process is gating or queued on the same PR
// refuses before the lock, naming that process.
func TestGateRefusesWhileAnotherGateRunsTheSamePR(t *testing.T) {
	w := newWorld(t, ghURL)
	env := w.env(false).WithDefaults()
	common, key, ok := env.gateRunKey(bg, w.dir, "w1")
	if !ok {
		t.Fatal("no gate run key")
	}
	other := os.Getppid() // a live process that is not this one
	os.MkdirAll(rotastate.Dir(common), 0o755)
	if err := os.WriteFile(rotastate.File(common, "gate-"+key)+".pid", []byte(strconv.Itoa(other)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var held bool
	lockErr := env.withLandLock(bg, w.dir, "", func(bool) error { // would block a gate that tried the lock
		held = true
		res, err := w.gate(false, GateOpts{})
		if err != nil || res.Verdict != GateRunning || res.Changed {
			t.Fatalf("verdict %q err %v: %s", res.Verdict, err, res.Err)
		}
		if !strings.Contains(res.Err, strconv.Itoa(other)) || strings.Contains(res.Err, "origin") {
			t.Errorf("message must name pid %d and not origin: %q", other, res.Err)
		}
		return nil
	})
	if lockErr != nil || !held {
		t.Fatalf("lock: %v", lockErr)
	}
}

// A stale marker (its process is gone) does not block.
func TestGateRunMarkerOfDeadProcessIsReplaced(t *testing.T) {
	common := t.TempDir()
	os.MkdirAll(rotastate.Dir(common), 0o755)
	if err := os.WriteFile(rotastate.File(common, "gate-pr1")+".pid", []byte("2147483646\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	other, release, err := claimGateRun(pidlive.Real{}, common, "pr1")
	if err != nil || other != 0 {
		t.Fatalf("claim: other=%d err=%v", other, err)
	}
	release()
}

// A marker whose pid is alive but belongs to a different process (pid reuse
// after a crash) does not block; the same process still does.
func TestGateRunMarkerOfReusedPidIsReplaced(t *testing.T) {
	ppid := os.Getppid()
	start, ok := pidlive.StartTicks(ppid)
	if !ok {
		t.Skip("no /proc start time on this platform")
	}
	for _, c := range []struct {
		name    string
		start   uint64
		blocked bool
	}{{"reused", start + 1, false}, {"same process", start, true}} {
		t.Run(c.name, func(t *testing.T) {
			common := t.TempDir()
			os.MkdirAll(rotastate.Dir(common), 0o755)
			body := fmt.Sprintf("%d %d\n", ppid, c.start)
			if err := os.WriteFile(rotastate.File(common, "gate-pr1")+".pid", []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			other, release, err := claimGateRun(pidlive.Real{}, common, "pr1")
			if err != nil {
				t.Fatal(err)
			}
			if c.blocked != (other == ppid) {
				t.Fatalf("other=%d, blocked want %v", other, c.blocked)
			}
			if release != nil {
				release()
			}
		})
	}
}

// #1125: the incident path for the false "no origin" of #1118. A second gate
// resolves the common dir, queues on the land lock, and finds its root gone
// only once it holds the lock (the first gate merged and removed it). The root
// is removed inside the lock, after the registry read, just before the remote
// probe, and the real git runner then fails to start in the gone cwd. Before
// #1118 stepRemote read any non-zero exit as a missing origin, and runGit maps
// a failed start to 127, so this exact state printed "no 'origin' remote".
// (A root gone before the lock fails earlier, at the git-common-dir lookup or
// the registry read, so that state cannot reach stepRemote.)
func TestGateGoneRootInsideLockIsNotAMissingOrigin(t *testing.T) {
	w := newWorld(t, ghURL)
	env := w.env(false).WithDefaults()
	env.Git = func(ctx context.Context, dir string, args ...string) (git.Result, error) {
		if len(args) > 0 && args[0] == "remote" {
			if err := os.RemoveAll(w.dir); err != nil {
				t.Fatal(err)
			}
		}
		return git.Exec(ctx, dir, args...)
	}
	res, err := env.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main"})
	if err != nil || res.Verdict != GateCheckBroke {
		t.Fatalf("verdict %q err %v", res.Verdict, err)
	}
	if strings.Contains(res.Err, "no 'origin' remote") || !strings.Contains(res.Err, "could not run git") {
		t.Errorf("want a git run failure, got %q", res.Err)
	}
}

// A marker whose holder has exited but not been reaped (a zombie) does not
// block: pidlive reads it as dead.
func TestGateRunMarkerOfZombieHolderIsReplaced(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Wait()
	pid := cmd.Process.Pid
	deadline := time.Now().Add(5 * time.Second)
	for pidlive.Alive(pid) {
		if time.Now().After(deadline) {
			t.Skip("child did not become a zombie (ps unavailable?)")
		}
		time.Sleep(10 * time.Millisecond)
	}
	common := t.TempDir()
	os.MkdirAll(rotastate.Dir(common), 0o755)
	if err := os.WriteFile(rotastate.File(common, "gate-pr1")+".pid", []byte(fmt.Sprintf("%d\n", pid)), 0o644); err != nil {
		t.Fatal(err)
	}
	other, release, err := claimGateRun(pidlive.Real{}, common, "pr1")
	if err != nil || other != 0 {
		t.Fatalf("zombie holder blocked the gate: other=%d err=%v", other, err)
	}
	release()
}

// fakeProcs is a recorded process table for markerLive: pid -> start ticks,
// absent means gone.
type fakeProcs map[int]uint64

func (f fakeProcs) State(pid int) (string, bool) { _, ok := f[pid]; return "S", ok }
func (f fakeProcs) Stat(pid int) (int, uint64, bool) {
	s, ok := f[pid]
	return 1, s, ok
}
func (fakeProcs) Comm(int) string              { return "" }
func (fakeProcs) Cwd(int) string               { return "" }
func (fakeProcs) Parents() (map[int]int, bool) { return nil, false }

// markerLive reads the process table it is given: a marker is live only for
// the same process, judged on a fake table with no real pid involved.
func TestMarkerLiveThroughInjectedSource(t *testing.T) {
	self := os.Getpid() // kill -0 must find a real pid; the table decides the rest
	cases := []struct {
		name  string
		table fakeProcs
		start uint64
		want  bool
	}{
		{"same start", fakeProcs{self: 7}, 7, true},
		{"reused pid", fakeProcs{self: 8}, 7, false},
		{"no recorded start", fakeProcs{self: 8}, 0, true},
	}
	for _, c := range cases {
		if got := markerLive(c.table, self, c.start); got != c.want {
			t.Errorf("%s: markerLive = %v, want %v", c.name, got, c.want)
		}
	}
	if m := gateMarker(fakeProcs{self: 42}, self); m != fmt.Sprintf("%d 42\n", self) {
		t.Errorf("gateMarker = %q", m)
	}
}
