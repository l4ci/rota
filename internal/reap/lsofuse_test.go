package reap

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/proc"
)

func TestParseLsofUses(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want []ProcUse
	}{
		{"one process", "p42\nn/tmp/x\n", []ProcUse{{PID: 42, Paths: []string{"/tmp/x"}}}},
		{"multi process", "p1\nn/a\nn/b\np2\nn/c\n", []ProcUse{{PID: 1, Paths: []string{"/a", "/b"}}, {PID: 2, Paths: []string{"/c"}}}},
		{"other fields ignored", "p7\nfcwd\ntDIR\ncfoo\nn/work\nf3\nn*:8080\nn->0xabc\n", []ProcUse{{PID: 7, Paths: []string{"/work"}}}},
		{"spaces in name", "p9\nn/tmp/a dir/my file\n", []ProcUse{{PID: 9, Paths: []string{"/tmp/a dir/my file"}}}},
		{"no paths", "p5\n", []ProcUse{{PID: 5}}},
		{"empty", "", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseLsofUses(tc.out); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseLsofUses = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestReadLsofUses(t *testing.T) {
	var got proc.Cmd
	ok := func(ctx context.Context, c proc.Cmd) (proc.Result, error) {
		got = c
		return proc.Result{Stdout: "p42\nn/tmp/x\n"}, nil
	}
	uses, err := ReadLsofUses(context.Background(), ok, 501)
	if err != nil || len(uses) != 1 || uses[0].PID != 42 {
		t.Fatalf("uses=%+v err=%v", uses, err)
	}
	if got.Name != "lsof" || strings.Join(got.Args, " ") != "-w -u 501 -Fpn" {
		t.Errorf("ran %s %v", got.Name, got.Args)
	}
	fails := map[string]proc.Runner{
		"absent": func(context.Context, proc.Cmd) (proc.Result, error) { return proc.Result{}, errors.New("not found") },
		"exit 1": func(context.Context, proc.Cmd) (proc.Result, error) {
			return proc.Result{ExitCode: 1, Stdout: "p1\nn/a\n"}, nil
		},
		"nothing": func(context.Context, proc.Cmd) (proc.Result, error) { return proc.Result{}, nil },
	}
	for name, run := range fails {
		if u, err := ReadLsofUses(context.Background(), run, 501); err == nil {
			t.Errorf("%s: want error, got %+v", name, u)
		}
	}
}

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
		return ReadLsofUses(bg, lsofRunner("p42\nn"+filepath.Join(dir, "run-a", "x")+"\n", nil, 0), 501)
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
			uses := func() ([]ProcUse, error) { return ReadLsofUses(bg, run, 501) }
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
