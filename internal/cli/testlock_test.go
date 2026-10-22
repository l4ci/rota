package cli

import (
	"context"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func needNice(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("nice"); err != nil {
		t.Skip("nice is not on PATH")
	}
}

func TestTestLockRunsTheCommandAndPassesItsExit(t *testing.T) {
	root := tierProject(t, `{}`)
	o := trRun(t, root, "", "test", "lock", "--", "echo", "held")
	if o.code != 0 || !strings.Contains(o.stdout, "held") {
		t.Fatalf("exit %d\n%s%s", o.code, o.stdout, o.stderr)
	}
	if o := trRun(t, root, "", "test", "lock", "--", "sh", "-c", "exit 3"); o.code != 3 {
		t.Errorf("a failing command's exit should be the verb's: got %d\n%s", o.code, o.stderr)
	}
	if o := trRun(t, root, "", "test", "lock"); o.code != 2 {
		t.Errorf("no command: exit %d, want 2", o.code)
	}
}

func TestTestLockMarksTheChildAsHolding(t *testing.T) {
	root := tierProject(t, `{}`)
	o := trRun(t, root, "", "test", "lock", "--", "sh", "-c", `echo "held=$ROTA_TEST_LOCK_HELD"`)
	if !strings.Contains(o.stdout, "held=1") {
		t.Fatalf("child env lacks the held mark:\n%s%s", o.stdout, o.stderr)
	}
}

func TestTestSlotsRejectsBadWidth(t *testing.T) {
	root := tierProject(t, `{"test":{"slots":0,"fast":["true"]}}`)
	if o := trRun(t, root, "", "test", "run", "fast"); o.code != 3 || !strings.Contains(o.stderr, "test.slots") {
		t.Errorf("slots 0: exit %d, stderr %q", o.code, o.stderr)
	}
}

func TestProofRecordRunsNicedAndHoldingTheLock(t *testing.T) {
	needNice(t)
	dir := backlogProject(t)
	code, out, _ := rotaIn(t, dir, "proof", "record", "B07", "--json", "--", `test "$ROTA_TEST_LOCK_HELD" = 1 && nice | grep -qx 10`)
	d := data(t, out)
	if code != 0 || d["niced"] != true {
		t.Fatalf("exit %d: %s", code, out)
	}
	if ev, _ := d["evidence"].(string); !strings.HasSuffix(ev, " niced=true") {
		t.Errorf("the row should say it was niced: %q", ev)
	}
}

func TestWorkerDoneTargetedProof(t *testing.T) {
	needNice(t)
	t.Run("runs the command and marks the row targeted", func(t *testing.T) {
		dir := doneProject(t, "false") // test.fast would fail; the targeted proof replaces it
		code, out, errOut := rotaIn(t, dir, "worker", "done", "ben", "--proof", "true", "--json")
		d := data(t, out)
		if code != 0 || d["targeted"] != true || d["changed"] != true {
			t.Fatalf("exit %d: %s %s", code, out, errOut)
		}
		_, shown, _ := rotaIn(t, dir, "proof", "show", "B07", "--json")
		if !strings.Contains(shown, "targeted=true") || !strings.Contains(shown, "niced=true") {
			t.Errorf("row lacks the targeted and niced marks: %s", shown)
		}
	})
	t.Run("a failing command refuses", func(t *testing.T) {
		dir := doneProject(t, "true")
		code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--proof", "false", "--json")
		if code != 4 || slotState(t, dir) == "done" {
			t.Fatalf("exit %d: %s", code, out)
		}
	})
}

func TestTestLockHeldMarkSkipsQueueingButNotConfigCheck(t *testing.T) {
	root := tierProject(t, `{"test":{"fast":["true"]}}`)
	t.Setenv("ROTA_TEST_LOCK_HELD", "1")
	// A slot taken by someone else: a run under the mark must not wait for it.
	env := testDeps().workerEnv()
	holding, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- env.WithTestLock(context.Background(), root, 1, "test run", io.Discard, func() error { close(holding); <-release; return nil })
	}()
	<-holding
	defer func() { close(release); <-done }()
	o := trRun(t, root, "", "test", "run", "fast")
	if o.code != 0 || strings.Contains(o.stderr, "waiting for the test lock") {
		t.Fatalf("held mark should bypass the queue: exit %d, stderr %q", o.code, o.stderr)
	}
	write(t, filepath.Join(root, ".rota", "config.json"), `{"test":{"slots":0,"fast":["true"]}}`)
	if o := trRun(t, root, "", "test", "run", "fast"); o.code != 3 || !strings.Contains(o.stderr, "test.slots") {
		t.Errorf("bad test.slots under the mark: exit %d, stderr %q", o.code, o.stderr)
	}
}

func TestTestLockSignalledChildExitsAs128PlusSignal(t *testing.T) {
	root := tierProject(t, `{}`)
	if o := trRun(t, root, "", "test", "lock", "--", "sh", "-c", "kill -TERM $$"); o.code != 143 {
		t.Errorf("signalled child: exit %d, want 143\n%s", o.code, o.stderr)
	}
	if o := trRun(t, root, "", "test", "lock", "--", "/no/such/command"); o.code != 5 {
		t.Errorf("start failure: exit %d, want 5\n%s", o.code, o.stderr)
	}
}

func TestWorkerDoneMarksAnExistingRowTargeted(t *testing.T) {
	needNice(t)
	dir := doneProject(t, "false")
	if code, out, _ := rotaIn(t, dir, "proof", "record", "B07", "--", "true"); code != 0 {
		t.Fatalf("record: %d %s", code, out)
	}
	code, out, errOut := rotaIn(t, dir, "worker", "done", "ben", "--proof", "true", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, out, errOut)
	}
	_, shown, _ := rotaIn(t, dir, "proof", "show", "B07", "--json")
	if !strings.Contains(shown, "niced=true targeted=true") {
		t.Errorf("the recorded row should carry targeted=true: %s", shown)
	}
}
