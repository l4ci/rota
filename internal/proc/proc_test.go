package proc

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunMapsExitCodeToResult(t *testing.T) {
	res, err := Run(context.Background(), Cmd{Name: "sh", Args: []string{"-c", "echo out; echo err >&2; exit 3"}})
	if err != nil || res.ExitCode != 3 || res.Stdout != "out\n" || res.Stderr != "err\n" {
		t.Errorf("%+v %v", res, err)
	}
}

func TestRunReportsExitWhileChildHoldsOutputPipes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		code     int
		group    bool
		combined bool
	}{
		{name: "success"},
		{name: "success combined group", group: true, combined: true},
		{name: "failure", code: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Cleanup(func() {
				b, err := os.ReadFile(filepath.Join(dir, "child.pid"))
				if err != nil {
					t.Error(err)
					return
				}
				pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
				if err != nil || pid <= 0 {
					t.Errorf("child pid %q: %v", b, err)
					return
				}
				_ = syscall.Kill(pid, syscall.SIGKILL)
			})
			res, err := Run(context.Background(), Cmd{
				Name: "sh", Args: []string{"-c", "sleep 30 & echo $! > child.pid; echo out; echo err >&2; exit " + strconv.Itoa(tc.code)},
				Dir: dir, Group: tc.group, Combined: tc.combined, Grace: 20 * time.Millisecond,
			})
			out, stderr := "out\n", "err\n"
			if tc.combined {
				out, stderr = "out\nerr\n", ""
			}
			if err != nil || res.ExitCode != tc.code || res.Stdout != out || res.Stderr != stderr {
				t.Fatalf("result %+v, err %v; want exit %d, stdout %q, stderr %q", res, err, tc.code, out, stderr)
			}
		})
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

// A command ended by ctx keeps what it wrote before, beside ctx.Err(), so a
// caller can show why it hung.
func TestRunTimeoutKeepsTheOutputSoFar(t *testing.T) {
	res, err := Run(context.Background(), Cmd{Name: "sh", Args: []string{"-c", "echo before; sleep 5"}, Timeout: 300 * time.Millisecond, Combined: true, Group: true})
	if !errors.Is(err, context.DeadlineExceeded) || res.Stdout != "before\n" {
		t.Errorf("%+v %v", res, err)
	}
}

func TestRunCancelKeepsTheOutputSoFar(t *testing.T) {
	deadline, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	ctx, cancel := context.WithCancel(deadline)
	defer cancel()
	dir := t.TempDir()
	ready := make(chan struct{})
	go func() {
		defer close(ready)
		for {
			if _, err := os.Stat(filepath.Join(dir, "ready")); err == nil {
				cancel()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()
	res, err := Run(ctx, Cmd{Name: "sh", Args: []string{"-c", "echo before; echo error >&2; touch ready; sleep 30"}, Dir: dir, Group: true})
	cancel()
	<-ready
	if !errors.Is(err, context.Canceled) || res.Stdout != "before\n" || res.Stderr != "error\n" {
		t.Fatalf("result %+v, err %v; want context.Canceled and partial output", res, err)
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

func TestRunStdioInheritsStreams(t *testing.T) {
	var out, errb bytes.Buffer
	r, err := Run(context.Background(), Cmd{Name: "sh", Args: []string{"-c", "read x; echo out:$x; echo err >&2; exit 3"},
		Stdio: &Stdio{In: strings.NewReader("hi\n"), Out: &out, Err: &errb}})
	if err != nil || r.ExitCode != 3 {
		t.Fatalf("result %+v err %v", r, err)
	}
	if out.String() != "out:hi\n" || errb.String() != "err\n" || r.Stdout != "" || r.Stderr != "" {
		t.Fatalf("out %q err %q result %+v", out.String(), errb.String(), r)
	}
}

func TestRunStdioReportsSignalAfterCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan int, 1)
	go func() { <-started; cancel() }()
	r, err := Run(ctx, Cmd{Name: "sh", Args: []string{"-c", "sleep 30"}, Grace: 10 * time.Second, Stdio: &Stdio{},
		Timeout: NoTimeout, OnStart: func(p *os.Process) { started <- p.Pid }})
	if !errors.Is(err, context.Canceled) || r.Signal != int(syscall.SIGTERM) {
		t.Fatalf("result %+v err %v", r, err)
	}
}

func TestRunDetachReturnsBeforeChildEnds(t *testing.T) {
	start := time.Now()
	var pid int
	r, err := Run(context.Background(), Cmd{Name: "sh", Args: []string{"-c", "sleep 2"}, Detach: true, Timeout: 50 * time.Millisecond,
		OnStart: func(p *os.Process) { pid = p.Pid }})
	if err != nil || r != (Result{}) || time.Since(start) > time.Second {
		t.Fatalf("result %+v err %v after %v", r, err, time.Since(start))
	}
	time.Sleep(200 * time.Millisecond) // past Timeout: the child must still be alive
	if pid <= 0 || syscall.Kill(pid, 0) != nil {
		t.Fatalf("detached child %d did not outlive Timeout", pid)
	}
	syscall.Kill(pid, syscall.SIGKILL)
}

func TestRunDetachStartFailure(t *testing.T) {
	_, err := Run(context.Background(), Cmd{Name: "rota-no-such-binary", Detach: true})
	if !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("err %v", err)
	}
}

func TestRunOnStartGetsPid(t *testing.T) {
	pid := 0
	Run(context.Background(), Cmd{Name: "true", OnStart: func(p *os.Process) { pid = p.Pid }})
	if pid <= 0 {
		t.Fatalf("pid %d", pid)
	}
}
