package cli

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/bugreport"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/receipt"
	"github.com/l4ci/rota/internal/rotastatetest"
	"github.com/l4ci/rota/internal/roundlease"
	"github.com/l4ci/rota/internal/roundtick"
)

func TestWindDownPrintsSummary(t *testing.T) {
	dir := workerProject(t, `{}`)
	seedLedger(t, dir)
	obj, text, ok := windDownSummary(dir, 2)
	if !ok || !strings.Contains(text, "Gate audit") || !strings.Contains(text, "ben") {
		t.Fatalf("ok=%v text:\n%s", ok, text)
	}
	if obj == nil {
		t.Fatal("no summary object")
	}
	if _, _, ok := windDownSummary(workerProject(t, `{}`), 2); ok {
		t.Error("a project with no ledger must add nothing")
	}
}

// A wind-down that errors after collecting warnings must still print them:
// the buffered-hits warning names the fold command the maintainer needs.
func TestWindDownPrintsWarningsOnError(t *testing.T) {
	dir := workerProject(t, `{}`)
	cd, err := rotastatetest.CommonDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	le := roundlease.DefaultEnv()
	if _, _, _, err := le.Acquire(cd, dir, roundlease.Holder{PID: os.Getpid(), Start: 1}, 1); err != nil {
		t.Fatal(err)
	}
	le.Alive = func(p int) bool { return p == os.Getpid() }
	le.StartTime = func(p int) (uint64, bool) { return 1, p == os.Getpid() }
	hits := filepath.Join(dir, ".rota", "knowledge-hits.jsonl")
	if err := os.WriteFile(hits, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// An unreadable registry makes WindDown fail after the warnings are set.
	if err := os.WriteFile(filepath.Join(dir, ".rota", "workers.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	old, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(old)
	d := testDeps()
	d.LeaseEnv = func() roundlease.Env { return le }
	var so, se strings.Builder
	code := mainWith(d, []string{"round", "wind-down", "--no-verify", "--holder-pid", strconv.Itoa(os.Getpid())}, strings.NewReader(""), &so, &se)
	if code == 0 {
		t.Fatalf("want an error exit, got 0: %s", so.String())
	}
	if !strings.Contains(se.String(), "rota knowledge tier fold") {
		t.Errorf("buffered-hits warning dropped on error (exit %d):\n%s", code, se.String())
	}
	// An error outcome resumes the autopilot (only a failed verify keeps it stopped).
	if roundtick.LoadState(cd, 1).Stopped {
		t.Error("an errored wind-down left the autopilot stopped")
	}
}

// The offer is one non-blocking line, printed only when open bug records exist.
func TestWindDownOffersBugreports(t *testing.T) {
	for _, records := range []int{0, 2} {
		dir := workerProject(t, `{}`)
		cd, err := rotastatetest.CommonDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		le := roundlease.DefaultEnv()
		if _, _, _, err := le.Acquire(cd, dir, roundlease.Holder{PID: os.Getpid(), Start: 1}, 1); err != nil {
			t.Fatal(err)
		}
		le.Alive = func(p int) bool { return p == os.Getpid() }
		le.StartTime = func(p int) (uint64, bool) { return 1, p == os.Getpid() }
		for i := 0; i < records; i++ {
			if _, err := bugreport.Add(context.Background(), git.Exec, dir, bugreport.Record{Source: bugreport.SourceAgent, Summary: "mistake " + strconv.Itoa(i)}, time.Now()); err != nil {
				t.Fatal(err)
			}
		}
		chdirForTest(t, dir)
		d := testDeps()
		d.LeaseEnv = func() roundlease.Env { return le }
		var so, se strings.Builder
		code := mainWith(d, []string{"round", "wind-down", "--no-verify", "--holder-pid", strconv.Itoa(os.Getpid())}, strings.NewReader(""), &so, &se)
		if code != 0 {
			t.Fatalf("%d records: exit %d\n%s%s", records, code, so.String(), se.String())
		}
		if has := strings.Contains(so.String(), "/rota-bugreport"); has != (records > 0) {
			t.Errorf("%d records: offer present=%v\n%s", records, has, so.String())
		}
	}
}

// A failed verify keeps the lease, so the round is not over: the tick memory
// and the receipts a retry replays must survive the wind-down.
func TestWindDownVerifyFailureKeepsTickStateAndReceipts(t *testing.T) {
	dir := workerProject(t, `{"test":{"full":["exit 3"]}}`)
	cd, err := rotastatetest.CommonDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	le := roundlease.DefaultEnv()
	if _, _, _, err := le.Acquire(cd, dir, roundlease.Holder{PID: os.Getpid(), Start: 1}, 1); err != nil {
		t.Fatal(err)
	}
	le.Alive = func(p int) bool { return p == os.Getpid() }
	le.StartTime = func(p int) (uint64, bool) { return 1, p == os.Getpid() }
	if err := roundtick.SaveState(cd, 1, roundtick.Result{Held: map[string]string{"ben": "12"}}); err != nil {
		t.Fatal(err)
	}
	rp := filepath.Join(dir, filepath.FromSlash(receipt.File))
	if err := os.MkdirAll(filepath.Dir(rp), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rp, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	chdirForTest(t, dir)
	d := testDeps()
	d.LeaseEnv = func() roundlease.Env { return le }
	var so, se strings.Builder
	code := mainWith(d, []string{"round", "wind-down", "--holder-pid", strconv.Itoa(os.Getpid())}, strings.NewReader(""), &so, &se)
	if !strings.Contains(so.String(), "verify-failed") {
		t.Fatalf("want a verify-failed verdict (exit %d):\n%s%s", code, so.String(), se.String())
	}
	if _, status, err := le.Read(cd); err != nil || status != roundlease.Live {
		t.Errorf("a failed verify keeps the lease: status %v, err %v", status, err)
	}
	st := roundtick.LoadState(cd, 1)
	if st.Held["ben"] != "12" || !st.Stopped {
		t.Errorf("tick state lost or not stopped after a failed verify: %+v", st)
	}
	if !strings.Contains(so.String(), "autopilot\tstays stopped") {
		t.Errorf("output lacks the autopilot-stopped line:\n%s", so.String())
	}
	if _, err := os.Stat(rp); err != nil {
		t.Errorf("receipts pruned after a failed verify: %v", err)
	}
}

// startedRound is a project with a live round (one slot, ben) held by this
// process and a tick state holding ben's merge for 12.
func startedRound(t *testing.T, testFull string) (dir, cd string, rnd int) {
	t.Helper()
	dir = workerProject(t, `{"git":{"baseBranch":"main"},"round":{"slots":1,"roster":["ben"]},"test":{"full":[`+testFull+`]}}`)
	os.WriteFile(filepath.Join(dir, ".rota", "BACKLOG.md"), []byte("# TODO\n\n## Bugs\n\n## Features\n\n## Tasks\n\n## Completed\n"), 0o644)
	if code, out, errOut := rotaStdin(t, dir, "", "round", "start", "--holder-pid", strconv.Itoa(os.Getpid()), "--base", "main", "--slots", "1"); code != 0 {
		t.Fatalf("round start: %d %s %s", code, out, errOut)
	}
	var err error
	if cd, err = rotastatetest.CommonDir(dir); err != nil {
		t.Fatal(err)
	}
	l, _, err := roundlease.DefaultEnv().Read(cd)
	if err != nil {
		t.Fatal(err)
	}
	if err := roundtick.SaveState(cd, l.Round, roundtick.Result{Held: map[string]string{"ben": "12"}}); err != nil {
		t.Fatal(err)
	}
	return dir, cd, l.Round
}

func windDown(t *testing.T, dir string, args ...string) (int, string, string) {
	t.Helper()
	return rotaStdin(t, dir, "", append([]string{"round", "wind-down", "--holder-pid", strconv.Itoa(os.Getpid())}, args...)...)
}

// Retained work with no verify failure resumes the autopilot and keeps the
// tick memory (the lease is kept for a retry).
func TestWindDownRetainedWorkResumesAutopilot(t *testing.T) {
	dir, cd, rnd := startedRound(t, `"true"`)
	os.WriteFile(filepath.Join(dir, ".worktrees", "ben", "wip.txt"), []byte("x"), 0o644)
	code, out, errOut := windDown(t, dir)
	if code != 4 {
		t.Fatalf("want exit 4 (holds work), got %d\n%s%s", code, out, errOut)
	}
	if st := roundtick.LoadState(cd, rnd); st.Stopped || st.Held["ben"] != "12" {
		t.Errorf("retained work after a green verify must resume the autopilot and keep the state: %+v", st)
	}
	// Without a verify nothing proves the base green: a prior stop stays.
	if err := roundtick.SetStopped(cd, rnd, true); err != nil {
		t.Fatal(err)
	}
	if code, out, errOut := windDown(t, dir, "--no-verify"); code != 4 || !roundtick.LoadState(cd, rnd).Stopped {
		t.Errorf("--no-verify with retained work must keep a prior stop (exit %d)\n%s%s", code, out, errOut)
	}
}

// A red verify followed by a later error still leaves the autopilot stopped.
func TestWindDownVerifyFailedThenErrorStaysStopped(t *testing.T) {
	dir, cd, rnd := startedRound(t, `"exit 3"`)
	// An unreadable registry fails WindDown after the verify went red.
	if err := os.WriteFile(filepath.Join(dir, ".rota", "workers.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := windDown(t, dir)
	if code == 0 || strings.Contains(out, "verdict") {
		t.Fatalf("want an error exit before any result (exit %d)\n%s%s", code, out, errOut)
	}
	if !roundtick.LoadState(cd, rnd).Stopped {
		t.Errorf("a red verify then an error must stay stopped\n%s%s", out, errOut)
	}
	if !strings.Contains(errOut, "autopilot stays stopped") {
		t.Errorf("error path lacks the stays-stopped line:\n%s", errOut)
	}
}

// A wind-down that fails before it verifies restores the stopped state it
// found: main may still be red from an earlier call.
func TestWindDownPreVerifyErrorRestoresStopped(t *testing.T) {
	dir, cd, rnd := startedRound(t, `"true"`)
	if err := roundtick.SetStopped(cd, rnd, true); err != nil {
		t.Fatal(err)
	}
	// A base that is not checked out is refused before the verify runs.
	gittest.Run(t, dir, "checkout", "-q", "-b", "elsewhere")
	code, out, errOut := windDown(t, dir)
	if code == 0 {
		t.Fatalf("want a refusal, got 0\n%s%s", out, errOut)
	}
	if !roundtick.LoadState(cd, rnd).Stopped {
		t.Errorf("a pre-verify error must restore the prior stopped state\n%s%s", out, errOut)
	}
}

// A tick on a stopped round says why (a red wind-down verify), not that the
// lease is gone.
func TestTickWhileStoppedNamesTheRedBase(t *testing.T) {
	dir, cd, rnd := startedRound(t, `"true"`)
	if err := roundtick.SetStopped(cd, rnd, true); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := rotaStdin(t, dir, "", "round", "tick", "--observe", "--holder-pid", strconv.Itoa(os.Getpid()))
	all := out + errOut
	if code == 0 || !strings.Contains(all, "failed the wind-down verify") || !strings.Contains(all, "rota round wind-down") || strings.Contains(all, "round start takes the lease") {
		t.Errorf("exit %d\n%s", code, all)
	}
}
