package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/proc"
)

// With no /proc the reader asks lsof (injected through Deps.Proc).
func TestProcessUsesReaderFallsBackToLsof(t *testing.T) {
	d := &Deps{Proc: func(ctx context.Context, c proc.Cmd) (proc.Result, error) {
		return proc.Result{Stdout: "p42\nn/tmp/x\n"}, nil
	}}
	uses, err := d.processUsesReader(filepath.Join(t.TempDir(), "no-proc"), 501)()
	if err != nil || len(uses) != 1 || uses[0].PID != 42 || uses[0].Paths[0] != "/tmp/x" {
		t.Fatalf("uses=%+v err=%v", uses, err)
	}
}

// Where /proc exists the lsof runner is never called.
func TestProcessUsesReaderKeepsProcWhenPresent(t *testing.T) {
	procRoot := t.TempDir()
	pid := filepath.Join(procRoot, "7")
	if err := os.MkdirAll(pid, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(pid, "cwd")); err != nil {
		t.Fatal(err)
	}
	d := &Deps{Proc: func(context.Context, proc.Cmd) (proc.Result, error) {
		t.Fatal("lsof ran although /proc exists")
		return proc.Result{}, nil
	}}
	uses, err := d.processUsesReader(procRoot, os.Getuid())()
	if err != nil || len(uses) != 1 || uses[0].PID != 7 {
		t.Fatalf("uses=%+v err=%v", uses, err)
	}
}

// defaultDeps wires the reader the host's /proc probe selects: lsof runs
// exactly when /proc is absent. Doctor counts leftovers with this same
// d.ProcessUses, as reap does.
func TestDefaultDepsProcessUsesIsSelected(t *testing.T) {
	_, statErr := os.Stat("/proc")
	lsofRan := false
	d := defaultDeps()
	d.Proc = func(context.Context, proc.Cmd) (proc.Result, error) {
		lsofRan = true
		return proc.Result{Stdout: "p1\nn/x\n"}, nil
	}
	// The /proc reader may fail on a read; only which reader ran matters.
	_, _ = d.ProcessUses()
	if want := statErr != nil; lsofRan != want {
		t.Errorf("lsof ran = %v, want %v (/proc stat err: %v)", lsofRan, want, statErr)
	}
}
