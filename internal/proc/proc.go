// Package proc is the one place rota runs an external binary and maps what
// happened to a result: exit-code mapping, the call timeout and the pipe wait
// delay live here, and the git, host, tracker, doctor and worker layers adapt
// it instead of each carrying their own exec wrapper.
package proc

import (
	"bytes"
	"context"
	"errors"
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
	// MaxOutput, when > 0, keeps at most that many bytes of stdout and of
	// stderr (of the shared buffer when Combined); the rest is read and
	// discarded so the child never blocks on a full pipe, and Result.Truncated
	// is set.
	MaxOutput int
}

// Runner runs one command. A command that ran and exited non-zero is a Result
// with ExitCode set and a nil error; err means it could not run at all (a
// missing binary, matching exec.ErrNotFound) or ctx ended it, in which case it
// is ctx.Err() and the Result holds only the output written before (no exit
// code), so a caller can show where a command hung.
type Runner func(ctx context.Context, c Cmd) (Result, error)

// Run is the production Runner.
func Run(ctx context.Context, c Cmd) (Result, error) {
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
	if c.Group {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	}
	cmd.WaitDelay = WaitDelay
	out, errb := &capBuf{max: c.MaxOutput}, &capBuf{max: c.MaxOutput}
	cmd.Stdout = out
	if c.Combined {
		cmd.Stderr = out
	} else {
		cmd.Stderr = errb
	}
	err := cmd.Run()
	if ctx.Err() != nil {
		return Result{Stdout: out.String(), Stderr: errb.String(), Truncated: out.cut || errb.cut}, ctx.Err()
	}
	r := Result{Stdout: out.String(), Stderr: errb.String(), Truncated: out.cut || errb.cut}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		r.ExitCode = ee.ExitCode()
		return r, nil
	}
	return r, err
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
