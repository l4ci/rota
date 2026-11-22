package cli

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
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
		t.Fatalf("uses=%+v err=%v (uid %s)", uses, err, strconv.Itoa(os.Getuid()))
	}
}

// The census doctor counts leftovers with is d.ProcessUses, the reader reap uses.
func TestDefaultDepsProcessUsesIsSelected(t *testing.T) {
	if defaultDeps().ProcessUses == nil {
		t.Fatal("defaultDeps leaves ProcessUses nil")
	}
}
