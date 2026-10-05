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
