package proc

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestRunMapsExitCodeToResult(t *testing.T) {
	res, err := Run(context.Background(), Cmd{Name: "sh", Args: []string{"-c", "echo out; echo err >&2; exit 3"}})
	if err != nil || res.ExitCode != 3 || res.Stdout != "out\n" || res.Stderr != "err\n" {
		t.Errorf("%+v %v", res, err)
	}
}

func TestRunMissingBinaryIsErrNotFound(t *testing.T) {
	_, err := Run(context.Background(), Cmd{Name: "rota-no-such-binary"})
	if !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}

func TestRunTimeoutReturnsContextError(t *testing.T) {
	res, err := Run(context.Background(), Cmd{Name: "sleep", Args: []string{"5"}, Timeout: 50 * time.Millisecond})
	if !errors.Is(err, context.DeadlineExceeded) || res != (Result{}) {
		t.Errorf("%+v %v", res, err)
	}
}

func TestRunGroupKillFreesPipeHeldByChild(t *testing.T) {
	start := time.Now()
	_, err := Run(context.Background(), Cmd{Name: "sh", Args: []string{"-c", "sleep 30 & wait"}, Timeout: 100 * time.Millisecond, Group: true})
	if err == nil || time.Since(start) > 3*time.Second {
		t.Errorf("err=%v after %v", err, time.Since(start))
	}
}

func TestRunCombinedAndEnvAndStdin(t *testing.T) {
	res, err := Run(context.Background(), Cmd{
		Name: "sh", Args: []string{"-c", "cat; echo $PROC_X >&2"}, Env: []string{"PROC_X=hi"},
		Stdin: []byte("in\n"), Combined: true,
	})
	if err != nil || res.ExitCode != 0 || !strings.Contains(res.Stdout, "in\n") || !strings.Contains(res.Stdout, "hi") || res.Stderr != "" {
		t.Errorf("%+v %v", res, err)
	}
}

func TestRunMaxOutputKeepsTheHeadAndDrainsTheRest(t *testing.T) {
	// 100000 bytes is more than a pipe buffer: a child blocked on a full pipe
	// would hang instead of exit 0.
	res, err := Run(context.Background(), Cmd{Name: "sh", Args: []string{"-c", "head -c 100000 /dev/zero | tr '\\0' x; echo err >&2; exit 2"}, MaxOutput: 10})
	if err != nil || res.ExitCode != 2 || res.Stdout != strings.Repeat("x", 10) || !res.Truncated {
		t.Errorf("%d %d %q %v %v", res.ExitCode, len(res.Stdout), res.Stdout, res.Truncated, err)
	}
	if res.Stderr != "err\n" {
		t.Errorf("stderr %q", res.Stderr)
	}
}

func TestRunMaxOutputCombinedAndStderrCapped(t *testing.T) {
	res, err := Run(context.Background(), Cmd{Name: "sh", Args: []string{"-c", "echo aaaa; echo bbbb >&2"}, Combined: true, MaxOutput: 6})
	if err != nil || len(res.Stdout) != 6 || !res.Truncated {
		t.Errorf("%q %v %v", res.Stdout, res.Truncated, err)
	}
	res, err = Run(context.Background(), Cmd{Name: "sh", Args: []string{"-c", "echo bbbbbbbb >&2"}, MaxOutput: 4})
	if err != nil || res.Stderr != "bbbb" || !res.Truncated {
		t.Errorf("%q %v %v", res.Stderr, res.Truncated, err)
	}
}

func TestRunMaxOutputUnderTheCapIsNotTruncated(t *testing.T) {
	res, err := Run(context.Background(), Cmd{Name: "echo", Args: []string{"hi"}, MaxOutput: 3})
	if err != nil || res.Stdout != "hi\n" || res.Truncated {
		t.Errorf("%q %v %v", res.Stdout, res.Truncated, err)
	}
	res, _ = Run(context.Background(), Cmd{Name: "echo", Args: []string{"hi"}})
	if res.Truncated {
		t.Error("no cap, truncated")
	}
}
