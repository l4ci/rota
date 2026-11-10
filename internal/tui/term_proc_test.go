package tui

import (
	"context"
	"io"
	"os"
	"slices"
	"testing"

	"github.com/l4ci/rota/internal/proc"
)

func TestSttyRunsThroughProcOnTheTerminalFile(t *testing.T) {
	old := runProc
	defer func() { runProc = old }()
	var got proc.Cmd
	runProc = func(_ context.Context, c proc.Cmd) (proc.Result, error) {
		got = c
		io.WriteString(c.Stdio.Out, " 24 80\n")
		return proc.Result{}, nil
	}
	out, err := stty(os.Stdin, "size")
	if err != nil || out != "24 80" || got.Name != "stty" || !slices.Equal(got.Args, []string{"size"}) || got.Stdio.In != os.Stdin {
		t.Fatalf("out %q err %v cmd %+v", out, err, got)
	}
	runProc = func(context.Context, proc.Cmd) (proc.Result, error) { return proc.Result{ExitCode: 1}, nil }
	if _, err := stty(os.Stdin, "size"); err == nil {
		t.Fatal("a failing stty must be an error")
	}
}
