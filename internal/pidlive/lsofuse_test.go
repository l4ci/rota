package pidlive

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

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
