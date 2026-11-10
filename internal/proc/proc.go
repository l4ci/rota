// Package proc is the one place rota runs an external binary and maps what
// happened to a result: exit-code mapping, the call timeout and the pipe wait
// delay live here, and the git, host, tracker, doctor and worker layers adapt
// it instead of each carrying their own exec wrapper.
package proc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// DefaultTimeout bounds a call whose Cmd sets no Timeout.
const DefaultTimeout = 2 * time.Minute

// NoTimeout as Cmd.Timeout leaves the call bounded by ctx alone.
const NoTimeout time.Duration = -1

// WaitDelay is how long a finished or killed command may keep the output pipes
// open (a killed gh or sh can leave a child holding them) before Run stops
// waiting for them.
const WaitDelay = 5 * time.Second

// Result is what a finished command left behind.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
	// Truncated says Cmd.MaxOutput cut stdout or stderr short.
	Truncated bool
	// Signal is the signal that killed the command, 0 when it exited. ExitCode
	// is -1 then. Set for a Stdio call even when ctx ended it.
	Signal int
}

// Stdio wires a command straight to the caller's streams: nothing is buffered
// or capped, and Result.Stdout and Stderr stay empty. A nil stream is the null
// device. Pass an *os.File to hand the child the file itself (a terminal),
// which also keeps Run from copying through a goroutine.
type Stdio struct {
	In       io.Reader
	Out, Err io.Writer
}

// Cmd is one call. Only Name is required.
type Cmd struct {
	Name string
	Args []string
	Dir  string // "" is the process cwd
	// Env is added to the process environment.
	Env []string
	// Environ, when non-nil, is the whole process environment, replacing
	// os.Environ and Env. An empty non-nil slice runs with no environment.
	Environ []string
	Stdin   []byte // nil gives the process an empty stdin
	// Timeout bounds the call; 0 is DefaultTimeout, NoTimeout is none.
	Timeout time.Duration
	// Combined sends stderr to the same buffer as stdout, returned in Stdout.
	Combined bool
	// Group runs the command in its own process group and kills the whole
	// group on cancel, for a shell whose child would otherwise hold the pipes.
	Group bool
	// Kill, with Group, replaces the group kill on cancel: it gets the
	// command's pid (the group's id) and must stop the group and whatever else
	// it owns, such as descendants that moved to their own group or session.
	Kill func(pid int) error
	// MaxOutput, when > 0, keeps at most that many bytes of stdout and of
	// stderr (of the shared buffer when Combined); the rest is read and
	// discarded so the child never blocks on a full pipe, and Result.Truncated
	// is set.
	MaxOutput int
	// Stdio, when set, runs the command on the caller's streams instead of
	// capturing output. Stdin, Combined and MaxOutput are ignored. Run does not
	// wait out WaitDelay for pipes the child's descendants keep open.
	Stdio *Stdio
	// Detach starts the command and returns at once with a zero Result; the
	// child is reaped in the background and its exit is not reported. Only a
	// failure to start is an error. Timeout and ctx do not reach a detached
	// child. Its stdio is the null device unless Stdio is set.
	Detach bool
	// Grace, when > 0, ends the command on cancel with SIGTERM and kills it
	// only after Grace; the default is an immediate kill.
	Grace time.Duration
	// OnStart, when set, is called with the pid once the command has started.
	OnStart func(pid int)
}

// Runner runs one command. A command that ran and exited non-zero is a Result
// with ExitCode set and a nil error; err means it could not run at all (a
// missing binary, matching exec.ErrNotFound) or ctx ended it, in which case it
// is ctx.Err() and the Result holds only the output written before (no exit
// code), so a caller can show where a command hung.
type Runner func(ctx context.Context, c Cmd) (Result, error)

// Run is the production Runner.
func Run(ctx context.Context, c Cmd) (Result, error) {
	if c.Detach {
		return detach(c)
	}
	switch {
	case c.Timeout == 0:
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultTimeout)
		defer cancel()
	case c.Timeout > 0:
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Dir = c.Dir
	if c.Environ != nil {
		cmd.Env = c.Environ
	} else if len(c.Env) > 0 {
		cmd.Env = append(os.Environ(), c.Env...)
	}
	if c.Stdin != nil {
		cmd.Stdin = bytes.NewReader(c.Stdin)
	}
	if c.Grace > 0 {
		cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	}
	if c.Group {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
		if c.Kill != nil {
			cmd.Cancel = func() error { return c.Kill(cmd.Process.Pid) }
		}
	}
	cmd.WaitDelay = WaitDelay
	if c.Grace > 0 {
		cmd.WaitDelay = c.Grace
	}
	out, errb := &capBuf{max: c.MaxOutput}, &capBuf{max: c.MaxOutput}
	if c.Stdio != nil {
		cmd.Stdin, cmd.Stdout, cmd.Stderr = c.Stdio.In, c.Stdio.Out, c.Stdio.Err
		if c.Grace <= 0 {
			cmd.WaitDelay = 0
		}
	} else {
		cmd.Stdout = out
		if c.Combined {
			cmd.Stderr = out
		} else {
			cmd.Stderr = errb
		}
	}
	err := start(cmd, c.OnStart)
	if err == nil {
		err = cmd.Wait()
	}
	r := Result{Stdout: out.String(), Stderr: errb.String(), Truncated: out.cut || errb.cut}
	var ee *exec.ExitError
	exited := errors.As(err, &ee)
	if exited && c.Stdio != nil {
		r.ExitCode, r.Signal = exitStatus(ee)
	}
	if ctx.Err() != nil {
		return r, ctx.Err()
	}
	if exited {
		r.ExitCode, r.Signal = exitStatus(ee)
		return r, nil
	}
	return r, err
}

// exitStatus is the exit code of a finished command, or -1 and the signal that
// killed it.
func exitStatus(ee *exec.ExitError) (code, signal int) {
	if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return -1, int(ws.Signal())
	}
	return ee.ExitCode(), 0
}

// start starts cmd and reports its pid to onStart.
func start(cmd *exec.Cmd, onStart func(pid int)) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	if onStart != nil {
		onStart(cmd.Process.Pid)
	}
	return nil
}

// detach starts c without ctx or a timeout and reaps it in the background.
func detach(c Cmd) (Result, error) {
	cmd := exec.Command(c.Name, c.Args...)
	cmd.Dir = c.Dir
	if c.Environ != nil {
		cmd.Env = c.Environ
	} else if len(c.Env) > 0 {
		cmd.Env = append(os.Environ(), c.Env...)
	}
	if c.Stdio != nil {
		cmd.Stdin, cmd.Stdout, cmd.Stderr = c.Stdio.In, c.Stdio.Out, c.Stdio.Err
	}
	if c.Group {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	if err := start(cmd, c.OnStart); err != nil {
		return Result{}, err
	}
	go cmd.Wait()
	return Result{}, nil
}

// capBuf is a buffer that keeps at most max bytes (no limit when max <= 0) and
// swallows the rest, so the pipe behind it keeps draining.
type capBuf struct {
	buf bytes.Buffer // not embedded: its ReadFrom would let io.Copy bypass Write
	max int
	cut bool
}

func (b *capBuf) String() string { return b.buf.String() }

func (b *capBuf) Write(p []byte) (int, error) {
	n := len(p)
	if b.max > 0 {
		room := b.max - b.buf.Len()
		if len(p) > room {
			p, b.cut = p[:room], true
		}
	}
	b.buf.Write(p)
	return n, nil
}
