package worker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/exitcode"
)

// setupRecorder is an Env.Shell that records each (dir, command) and exits
// with the code the test sets.
type setupRecorder struct {
	runs []string
	code int
}

func (r *setupRecorder) shell(_ context.Context, dir, command string) (string, int) {
	r.runs = append(r.runs, dir+"|"+command)
	if r.code != 0 {
		return "npm ERR! boom", r.code
	}
	return "", 0
}

func setupInit(t *testing.T, r *setupRecorder, dir string, slots int) error {
	t.Helper()
	_, err := Env{Shell: r.shell}.PoolInit(bg, dir, InitOpts{Slots: slots, Base: "main"}, &Accounts{})
	return err
}

func TestEnvSetupRunsOncePerNewSlotInItsWorktree(t *testing.T) {
	b := newProject(t, `{"work":{"envSetup":"npm ci"}}`)
	r := &setupRecorder{}
	if err := setupInit(t, r, b, 2); err != nil {
		t.Fatal(err)
	}
	if len(r.runs) != 2 {
		t.Fatalf("runs = %v, want one per new slot", r.runs)
	}
	for i, name := range []string{"w1", "w2"} {
		want := realPath(filepath.Join(b, ".worktrees", name)) + "|npm ci"
		if r.runs[i] != want {
			t.Errorf("run %d = %q, want %q", i, r.runs[i], want)
		}
	}
	// Unchanged lockfiles: a re-run and a grow only set up the new slot.
	if err := setupInit(t, r, b, 3); err != nil {
		t.Fatal(err)
	}
	if len(r.runs) != 3 || !strings.Contains(r.runs[2], "w3") {
		t.Errorf("runs after grow = %v, want only w3 added", r.runs)
	}
}

func TestEnvSetupEmptyIsNoop(t *testing.T) {
	b := newProject(t, `{}`)
	r := &setupRecorder{}
	if err := setupInit(t, r, b, 1); err != nil {
		t.Fatal(err)
	}
	if len(r.runs) != 0 {
		t.Errorf("runs = %v, want none", r.runs)
	}
}

func TestEnvSetupRerunsWhenALockfileChanges(t *testing.T) {
	b := newProject(t, `{"work":{"envSetup":"npm ci"}}`)
	r := &setupRecorder{}
	if err := setupInit(t, r, b, 1); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(b, ".worktrees", "w1")
	for _, f := range []string{"go.sum", "requirements-dev.txt"} {
		before := len(r.runs)
		os.WriteFile(filepath.Join(wt, f), []byte(f+" v1\n"), 0o644)
		if err := setupInit(t, r, b, 1); err != nil {
			t.Fatal(err)
		}
		if len(r.runs) != before+1 {
			t.Errorf("%s changed: runs = %v, want a rerun", f, r.runs)
		}
		if err := setupInit(t, r, b, 1); err != nil || len(r.runs) != before+1 {
			t.Errorf("%s unchanged: runs = %v err=%v, want a skip", f, r.runs, err)
		}
	}
	// A file that is not a lockfile does not count.
	before := len(r.runs)
	os.WriteFile(filepath.Join(wt, "README.md"), []byte("x"), 0o644)
	setupInit(t, r, b, 1)
	if len(r.runs) != before {
		t.Errorf("a non-lockfile change re-ran setup: %v", r.runs)
	}
}

func TestEnvSetupFailsFastAndStoresNoHash(t *testing.T) {
	b := newProject(t, `{"work":{"envSetup":"npm ci"}}`)
	r := &setupRecorder{code: 3}
	err := setupInit(t, r, b, 2)
	ee, ok := err.(*exitcode.Error)
	if !ok || ee.Exit != exitcode.ExitFailed {
		t.Fatalf("err = %#v, want exit %d", err, exitcode.ExitFailed)
	}
	for _, want := range []string{"slot w1", "npm ci", "npm ERR! boom"} {
		if !strings.Contains(ee.Message, want) {
			t.Errorf("message %q lacks %q", ee.Message, want)
		}
	}
	if len(r.runs) != 1 {
		t.Errorf("runs = %v, want fail-fast after the first slot", r.runs)
	}
	// No hash was stored, so the next init retries and, once green, records it.
	r.code = 0
	r.runs = nil
	if err := setupInit(t, r, b, 2); err != nil {
		t.Fatal(err)
	}
	if len(r.runs) != 2 {
		t.Errorf("retry runs = %v, want w1 retried and w2 set up", r.runs)
	}
	if err := setupInit(t, r, b, 2); err != nil || len(r.runs) != 2 {
		t.Errorf("green slots re-ran: %v err=%v", r.runs, err)
	}
}

func TestEnvSetupRedRerunDropsTheOldHash(t *testing.T) {
	b := newProject(t, `{"work":{"envSetup":"npm ci"}}`)
	r := &setupRecorder{}
	setupInit(t, r, b, 1)
	wt := filepath.Join(b, ".worktrees", "w1")
	os.WriteFile(filepath.Join(wt, "yarn.lock"), []byte("v2"), 0o644)
	r.code = 1
	if err := setupInit(t, r, b, 1); err == nil {
		t.Fatal("want the red rerun to fail")
	}
	// The lockfile is still v2 and the last run was red: it must run again.
	r.code = 0
	n := len(r.runs)
	if err := setupInit(t, r, b, 1); err != nil || len(r.runs) != n+1 {
		t.Errorf("runs = %v err=%v, want a retry after the red run", r.runs, err)
	}
}
