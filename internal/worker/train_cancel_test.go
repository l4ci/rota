package worker

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/pidlive"
	"github.com/l4ci/rota/internal/rotastate"
)

// waitFile polls for path until it exists or the deadline passes.
func waitFile(t *testing.T, path string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never appeared", path)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// readPid reads a pid file a verify command wrote, and kills that process when
// the test ends so a failing run leaves nothing behind.
func readPid(t *testing.T, path string) int {
	t.Helper()
	waitFile(t, path, 10*time.Second)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatalf("pid file %s: %q", path, b)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	return pid
}

// gone waits briefly for pid to die: SIGKILL is delivered asynchronously.
func gone(pid int) bool {
	for i := 0; i < 100; i++ {
		if !pidlive.Alive(pid) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// A cancel during a child test command stops that command's whole process
// tree, including a server that moved to its own session, removes the scratch
// tree, frees the land lock and settles as cancelled with nothing landed and
// nothing blamed (#2751, AC-2 to AC-5 and AC-8).
func TestTrainCancelDuringVerify(t *testing.T) {
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("setsid not installed")
	}
	pids := t.TempDir()
	verify := "setsid sh -c 'echo $$ > " + pids + "/srv.pid; exec sleep 300' </dev/null >/dev/null 2>&1 & " +
		"echo $$ > " + pids + "/sh.pid; touch " + pids + "/started; sleep 300"
	w := trainWorld(t, verify, "b1", "b2")
	ctx, cancel := context.WithCancel(bg)
	defer cancel()
	go func() {
		waitFile(t, filepath.Join(pids, "started"), 30*time.Second)
		cancel()
	}()
	res, err := w.env(false).Train(ctx, w.dir, TrainOpts{Base: "main", Targets: []string{"b1", "b2"}})
	srv, sh := readPid(t, filepath.Join(pids, "srv.pid")), readPid(t, filepath.Join(pids, "sh.pid"))
	if err != nil {
		t.Fatalf("a cancelled train is an answer, not an error: %v", err)
	}
	if res.Verdict != GateCancelled {
		t.Fatalf("verdict %q, want %q: %+v", res.Verdict, GateCancelled, res)
	}
	if len(res.Landed) != 0 || res.Changed || w.onMain("b1.txt") {
		t.Errorf("a cancel before landing lands nothing: landed %v changed %v", res.Landed, res.Changed)
	}
	if res.Culprit != "" || len(res.Culprits) != 0 {
		t.Errorf("a cancel blames nobody: culprit %q culprits %v", res.Culprit, res.Culprits)
	}
	for _, m := range res.Members {
		if m.Culprit {
			t.Errorf("member %s marked culprit", m.Target)
		}
	}
	if !strings.HasPrefix(res.Err, "CANCELLED train") || !strings.Contains(res.Err, "nothing landed") {
		t.Errorf("err %q", res.Err)
	}
	if res.Log == "" {
		t.Fatalf("the result names no log")
	}
	if b, err := os.ReadFile(res.Log); err != nil || !strings.Contains(string(b), "sleep 300") {
		t.Errorf("the verify log %s is not kept: %v %q", res.Log, err, b)
	}
	os.Remove(res.Log)
	if !gone(sh) {
		t.Errorf("the verify shell %d outlived the cancel", sh)
	}
	if !gone(srv) {
		t.Errorf("the server %d in its own session outlived the cancel", srv)
	}
	if out := gittest.Run(t, w.dir, "worktree", "list"); strings.Count(out, "\n") != 0 {
		t.Errorf("scratch worktree left behind:\n%s", out)
	}
	if held, err := w.env(false).WithDefaults().LandLockHeld(bg, w.dir); held || err != nil {
		t.Errorf("land lock still held: %v %v", held, err)
	}
}

// A train queued behind another landing stops waiting for the land lock when
// it is cancelled, instead of waiting up to the lock timeout (#2751, AC-1).
func TestTrainCancelWhileQueued(t *testing.T) {
	w := trainWorld(t, "true", "b1")
	env := w.env(false).WithDefaults()
	held, release := make(chan struct{}), make(chan struct{})
	go env.withLandLock(bg, w.dir, "other", func(bool) error {
		close(held)
		<-release
		return nil
	})
	defer close(release)
	<-held
	ctx, cancel := context.WithCancel(bg)
	done := make(chan TrainResult, 1)
	go func() {
		res, _ := env.Train(ctx, w.dir, TrainOpts{Base: "main", Targets: []string{"b1"}})
		done <- res
	}()
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case res := <-done:
		if res.Verdict != GateCancelled || len(res.Landed) != 0 {
			t.Errorf("queued cancel: %+v", res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled train kept waiting for the land lock")
	}
}

// A cancel after some members landed reports exactly those as landed and
// blames nobody (#2751, AC-4).
func TestCancelledTrainKeepsLanded(t *testing.T) {
	res := TrainResult{Base: "main", Verdict: GateMergeFailed, Culprit: "b2", Landed: []string{"b1"}, Changed: true,
		Members:  []TrainMember{{Target: "b1", Landed: true}, {Target: "b2", Culprit: true}},
		Culprits: []TrainCulprit{{Target: "b2"}}}
	got := cancelledTrain(res)
	if got.Verdict != GateCancelled || got.Culprit != "" || got.Culprits != nil || got.Members[1].Culprit {
		t.Fatalf("%+v", got)
	}
	if strings.Join(got.Landed, ",") != "b1" || !got.Members[0].Landed || !got.Changed {
		t.Errorf("landed members lost: %+v", got)
	}
	if !strings.Contains(got.Err, "landed 1 of 2 member(s): b1") {
		t.Errorf("err %q", got.Err)
	}
	if m := ClassifyVerdict(GateCancelled); m.Success || m.Refusal || m.Notify || m.Hold {
		t.Errorf("cancelled must be a plain non-pass answer: %+v", m)
	}
}

// writeTrainRun plants a live marker for id naming pid, as a train would.
func writeTrainRun(t *testing.T, env Env, root, id string, pid int) {
	t.Helper()
	common, err := rotastate.CommonDirVia(bg, env.Git, root)
	if err != nil {
		t.Fatal(err)
	}
	_, st, _ := env.procs().Stat(pid)
	path := trainRunFile(common, id)
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := fsio.WriteJSONAtomic(path, TrainRun{PID: pid, Start: st, RequestID: id, Targets: []string{"b1"}, Base: "main", Since: "2026-11-07T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
}

// CancelTrain finds the process holding the id, sends it SIGTERM and waits
// for it to exit; an id nobody holds cancels nothing (#2751, AC-1).
func TestCancelTrainSignalsTheHolder(t *testing.T) {
	w := trainWorld(t, "true", "b1")
	env := w.env(false).WithDefaults()
	if _, err := env.CancelTrain(bg, w.dir, "req-none", time.Second); !errors.Is(err, ErrTrainNotRunning) {
		t.Fatalf("unknown id: %v", err)
	}
	holder := exec.Command("sleep", "30")
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { holder.Process.Kill() })
	exited := make(chan error, 1)
	go func() { exited <- holder.Wait() }()
	writeTrainRun(t, env, w.dir, "req-live", holder.Process.Pid)
	runs, err := env.TrainRuns(bg, w.dir)
	if err != nil || len(runs) != 1 || runs[0].RequestID != "req-live" {
		t.Fatalf("live trains: %+v %v", runs, err)
	}
	run, err := env.CancelTrain(bg, w.dir, "req-live", 10*time.Second)
	if err != nil || run.PID != holder.Process.Pid {
		t.Fatalf("cancel: %+v %v", run, err)
	}
	select {
	case err := <-exited:
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.Sys().(syscall.WaitStatus).Signal() != syscall.SIGTERM {
			t.Errorf("the holder was not stopped by SIGTERM: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the holder is still running")
	}
	// A marker of a process that is gone is no running train.
	if _, err := env.CancelTrain(bg, w.dir, "req-live", time.Second); !errors.Is(err, ErrTrainNotRunning) {
		t.Errorf("a finished train is not running: %v", err)
	}
}
