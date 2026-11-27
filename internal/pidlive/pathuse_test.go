package pidlive

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/proc"
)

// With no /proc the reader asks lsof (injected through the process runner).
func TestProcessUsesReaderFallsBackToLsof(t *testing.T) {
	run := func(ctx context.Context, c proc.Cmd) (proc.Result, error) {
		return proc.Result{Stdout: "p42\nn/tmp/x\n"}, nil
	}
	uses, err := readPathUses(context.Background(), filepath.Join(t.TempDir(), "no-proc"), run, 501)
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
	run := func(context.Context, proc.Cmd) (proc.Result, error) {
		t.Fatal("lsof ran although /proc exists")
		return proc.Result{}, nil
	}
	uses, err := readPathUses(context.Background(), procRoot, run, os.Getuid())
	if err != nil || len(uses) != 1 || uses[0].PID != 7 {
		t.Fatalf("uses=%+v err=%v", uses, err)
	}
}
