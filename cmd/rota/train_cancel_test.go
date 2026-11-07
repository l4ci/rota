package main

import (
	"bytes"
	"encoding/json"
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
	"github.com/l4ci/rota/internal/pidlive"
	"github.com/l4ci/rota/internal/rotastate"
)

// trainProject is a repo on main with branch t1 and a slot for it, whose
// test.full starts a server in its own session and then blocks.
func trainProject(t *testing.T) (proj, pids string) {
	t.Helper()
	base := t.TempDir()
	proj, pids = filepath.Join(base, "proj"), filepath.Join(base, "pids")
	os.MkdirAll(filepath.Join(proj, ".rota"), 0o755)
	os.MkdirAll(pids, 0o755)
	git(t, proj, "init", "-q", "-b", "main", ".")
	commitFile(t, proj, "seed", "seed.txt", "seed\n")
	git(t, proj, "checkout", "-q", "-b", "t1")
	commitFile(t, proj, "add t1", "t1.txt", "t1\n")
	git(t, proj, "checkout", "-q", "main")
	write(t, proj, ".rota/workers.json", `{"slots":[{"name":"t1","branch":"t1"}]}`+"\n")
	verify := "setsid sh -c 'echo $$ > " + pids + "/srv.pid; exec sleep 300' </dev/null >/dev/null 2>&1 & " +
		"touch " + pids + "/started; sleep 300"
	cfg, _ := json.Marshal(map[string]any{"ship": map[string]any{"review": "none"}, "test": map[string]any{"full": []string{verify}}})
	write(t, proj, ".rota/config.json", string(cfg)+"\n")
	return proj, pids
}

type bgTrain struct {
	cmd      *exec.Cmd
	out, err bytes.Buffer
}

// startTrain runs `rota worker train t1` in the background under id and waits
// until its test command is running.
func startTrain(t *testing.T, proj, pids, id string) *bgTrain {
	t.Helper()
	os.Remove(filepath.Join(pids, "started"))
	b := &bgTrain{cmd: exec.Command(rotaBin, "--json", "worker", "train", "t1", "--base", "main", "--no-assign-check", "--request-id", id)}
	b.cmd.Dir, b.cmd.Env = proj, baseEnv
	b.cmd.Stdout, b.cmd.Stderr = &b.out, &b.err
	if err := b.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.cmd.Process.Kill() })
	deadline := time.Now().Add(60 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(pids, "started")); err == nil {
			return b
		}
		if time.Now().After(deadline) {
			t.Fatalf("the train's test command never started\n%s", b.err.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// finish waits for the train and returns its exit code and JSON envelope.
func (b *bgTrain) finish(t *testing.T) (int, envl) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- b.cmd.Wait() }()
	var err error
	select {
	case err = <-done:
	case <-time.After(60 * time.Second):
		t.Fatalf("the train did not exit after the cancel\n%s", b.err.String())
	}
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return code, parseEnv(t, "worker train", run{code, b.out.String(), b.err.String(), ""})
}

// assertCancelledTrain checks the outcomes every cancel path must leave: verdict
// cancelled, nothing landed, the log kept, no server, no scratch tree, the land
// lock free and the receipt settled as cancelled (#2751, AC-2 to AC-7).
func assertCancelledTrain(t *testing.T, proj, pids, id string, code int, e envl) {
	t.Helper()
	if code != 1 || at(e, "data.verdict") != "cancelled" {
		t.Fatalf("want exit 1 with verdict cancelled, got %d: %v", code, e)
	}
	if landed, _ := at(e, "data.landed").([]any); len(landed) != 0 || at(e, "data.changed") != false {
		t.Errorf("nothing may land: %v", e)
	}
	if !strings.Contains(at(e, "error.message").(string), "CANCELLED train") {
		t.Errorf("message: %v", at(e, "error"))
	}
	logPath, _ := at(e, "data.log").(string)
	if b, err := os.ReadFile(logPath); err != nil || !strings.Contains(string(b), "sleep 300") {
		t.Errorf("the log %q is not kept: %v", logPath, err)
	}
	b, err := os.ReadFile(filepath.Join(pids, "srv.pid"))
	if err != nil {
		t.Fatal(err)
	}
	srv, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	t.Cleanup(func() { syscall.Kill(srv, syscall.SIGKILL) })
	for i := 0; i < 100 && pidlive.Alive(srv); i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if pidlive.Alive(srv) {
		t.Errorf("the server %d outlived the cancelled train", srv)
	}
	if out := git(t, proj, "worktree", "list"); strings.Count(strings.TrimSpace(out), "\n") != 0 {
		t.Errorf("scratch worktree left behind:\n%s", out)
	}
	if err := fsio.Locked(rotastate.File(filepath.Join(proj, ".git"), "land"), 0, func() error { return nil }); err != nil {
		t.Errorf("the land lock is still held: %v", err)
	}
	r := exec1e(t, proj, "", nil, rotaBin, "--json", "receipt", "show", id)
	if s := at(parseEnv(t, "receipt show", r), "data.state"); r.code != 0 || s != "cancelled" {
		t.Errorf("receipt show: exit %d state %v\n%s", r.code, s, r.stdout)
	}
	r = exec1e(t, proj, "", nil, rotaBin, "--json", "round", "status")
	if v := at(parseEnv(t, "round status", r), "data.lastTrain.verdict"); v != "cancelled" {
		t.Errorf("round status lastTrain: %v\n%s", v, r.stdout)
	}
}

// `rota worker train cancel --request-id` stops the train holding the id while
// its test command runs, and the train settles as cancelled (#2751, AC-1, AC-8).
func TestTrainCancelByRequestID(t *testing.T) {
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("setsid not installed")
	}
	proj, pids := trainProject(t)
	b := startTrain(t, proj, pids, "req-e2e")
	r := exec1e(t, proj, "", nil, rotaBin, "--json", "round", "status")
	if id := at(parseEnv(t, "round status", r), "data.trains.0.requestId"); id != "req-e2e" {
		t.Errorf("round status must list the running train: %v\n%s", id, r.stdout)
	}
	c := exec1e(t, proj, "", nil, rotaBin, "--json", "worker", "train", "cancel", "--request-id", "req-e2e")
	ce := parseEnv(t, "train cancel", c)
	if c.code != 0 || at(ce, "data.state") != "cancelled" || at(ce, "data.result.verdict") != "cancelled" {
		t.Errorf("cancel: exit %d %v\n%s", c.code, ce, c.stderr)
	}
	code, e := b.finish(t)
	assertCancelledTrain(t, proj, pids, "req-e2e", code, e)
	again := exec1e(t, proj, "", nil, rotaBin, "worker", "train", "cancel", "--request-id", "req-e2e")
	if again.code != 4 || !strings.Contains(again.stderr, "already settled as cancelled") {
		t.Errorf("a second cancel must refuse: exit %d %s", again.code, again.stderr)
	}
}

// SIGINT to the train process takes the same path as the cancel verb: the
// cancelled result, not CHECK-BROKE (#2751, AC-6).
func TestTrainInterruptIsACancel(t *testing.T) {
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("setsid not installed")
	}
	proj, pids := trainProject(t)
	b := startTrain(t, proj, pids, "req-int")
	if err := b.cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	code, e := b.finish(t)
	assertCancelledTrain(t, proj, pids, "req-int", code, e)
}
