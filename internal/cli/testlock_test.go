package cli

import (
	"os/exec"
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
