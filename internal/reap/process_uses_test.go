package reap

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/pidlive"
	"github.com/l4ci/rota/internal/proc"
)

func lsofRunner(out string, err error, exit int) proc.Runner {
	return func(context.Context, proc.Cmd) (proc.Result, error) {
		return proc.Result{Stdout: out, ExitCode: exit}, err
	}
}

// With lsof as the reader, a tmp entry a process holds is held and an old idle
// sibling is reaped (the no-/proc path).
func TestTmpWithLsofReaderHoldsUsedAndReapsIdle(t *testing.T) {
	dir := tmpRoot(t, map[string]time.Duration{"run-a": 5 * time.Hour, "run-b": 5 * time.Hour})
	in := tmpInput(t, dir, nil, nil)
	in.ProcessUses = func() ([]ProcUse, error) {
		return pidlive.ReadLsofUses(bg, lsofRunner("p42\nn"+filepath.Join(dir, "run-a", "x")+"\n", nil, 0), 501)
	}
	res := find(t, in, KindTmp)
	held := map[string]string{}
	for _, c := range res.Candidates {
		held[c.ID] = c.Held
	}
	if !strings.Contains(held["tmp:run-a"], "live process 42 uses") || held["tmp:run-b"] != "" {
		t.Fatalf("held = %v", held)
	}
	if applied := Apply(bg, in, res.Candidates); len(applied.Reaped) != 1 || applied.Reaped[0] != "tmp:run-b" {
		t.Fatalf("apply = %+v", applied)
	}
}

// An lsof that is absent, fails or prints nothing holds tmp and systmp candidates.
func TestLsofFailureHoldsTmpAndSysTmp(t *testing.T) {
	runners := map[string]proc.Runner{
		"absent":  lsofRunner("", errors.New("executable file not found"), 0),
		"failed":  lsofRunner("p1\nn/x\n", nil, 1),
		"nothing": lsofRunner("", nil, 0),
	}
	for name, run := range runners {
		t.Run(name, func(t *testing.T) {
			uses := func() ([]ProcUse, error) { return pidlive.ReadLsofUses(bg, run, 501) }
			dir := tmpRoot(t, map[string]time.Duration{"run-a": 5 * time.Hour})
			in := tmpInput(t, dir, nil, nil)
			in.ProcessUses = uses
			sys := sysTemp(t, map[string]time.Duration{"rota-smoke.old": 3 * time.Hour})
			sysIn := sysInput(t, sys, nil)
			sysIn.ProcessUses = uses
			for kind, i := range map[string]Input{KindTmp: in, KindSysTmp: sysIn} {
				res := find(t, i, kind)
				if len(res.Candidates) != 1 || !strings.Contains(res.Candidates[0].Held, "cannot prove no process uses it") {
					t.Fatalf("%s candidates = %+v", kind, res.Candidates)
				}
				if applied := Apply(bg, i, res.Candidates); len(applied.Reaped) != 0 {
					t.Errorf("%s reaped %+v", kind, applied.Reaped)
				}
			}
		})
	}
}
