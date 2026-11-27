package cli

import (
	"context"
	"os"
	"testing"

	"github.com/l4ci/rota/internal/proc"
)

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
