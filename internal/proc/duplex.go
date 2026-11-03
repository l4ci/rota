package proc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
)

// Exchange runs a bounded conversation with a subprocess. The callback must
// return when its reads or writes fail; it must not retain either endpoint.
type Exchange func(context.Context, Cmd, func(io.Reader, io.Writer) error) error

// ErrOutputLimit means the subprocess exceeded the conversation's total stdout
// budget. Neither stdout nor stderr is included in this error.
var ErrOutputLimit = errors.New("process stdout limit exceeded")

// Duplex is the production Exchange. Timeout, environment and Group follow Run.
// MaxOutput bounds total stdout (default 1 MiB); stderr is always discarded.
// Stdin and Combined are ignored: talk owns stdin and reads only stdout.
// Completing talk always closes the pipes, kills the child (and its group when
// requested), and reaps it. A successful protocol need not wait for child exit.
func Duplex(ctx context.Context, c Cmd, talk func(io.Reader, io.Writer) error) error {
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
	if c.Group {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	cmd.WaitDelay = WaitDelay
	// A nil Stderr goes directly to the null device, without a copy goroutine
	// that a surviving descendant could keep blocked.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, childOut, err := os.Pipe()
	if err != nil {
		stdin.Close()
		return err
	}
	cmd.Stdout = childOut
	max := c.MaxOutput
	if max <= 0 {
		max = 1 << 20
	}
	stream := &exchangeOutput{max: max}
	stream.ready = sync.NewCond(&stream.mu)
	stop := sync.OnceFunc(func() {
		stream.finish(ctx.Err())
		stdin.Close()
		stdout.Close()
		if cmd.Process != nil {
			if c.Group {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			} else {
				_ = cmd.Process.Kill()
			}
		}
	})
	cmd.Cancel = func() error { stop(); return nil }
	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		childOut.Close()
		return err
	}
	childOut.Close()
	pumpDone := make(chan struct{})
	go func() {
		defer close(pumpDone)
		_, err := io.Copy(stream, stdout)
		stream.finish(err)
		if errors.Is(err, ErrOutputLimit) {
			stop()
		}
	}()
	cleanup := sync.OnceFunc(func() {
		stop()
		<-pumpDone
		// Intentional cleanup kills a still-running child even on protocol
		// success. Its exit status is not the conversation's result.
		_ = cmd.Wait()
	})
	defer cleanup()
	err = talk(stream, stdin)
	cleanup()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if stream.limited {
		return ErrOutputLimit
	}
	return err
}

// exchangeOutput buffers at most max bytes over the entire conversation, even
// when the consumer drains it incrementally. The pump never waits for a reader,
// so excess output interrupts a callback blocked on either endpoint.
type exchangeOutput struct {
	mu      sync.Mutex
	ready   *sync.Cond
	buf     bytes.Buffer
	max     int
	total   int
	limited bool
	err     error
}

func (s *exchangeOutput) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return 0, s.err
	}
	n := len(p)
	if n > s.max-s.total {
		n = s.max - s.total
		s.limited = true
		s.err = ErrOutputLimit
	}
	s.buf.Write(p[:n])
	s.total += n
	s.ready.Broadcast()
	if s.limited {
		return n, ErrOutputLimit
	}
	return n, nil
}

func (s *exchangeOutput) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.buf.Len() == 0 && s.err == nil {
		s.ready.Wait()
	}
	if s.buf.Len() > 0 {
		return s.buf.Read(p)
	}
	return 0, s.err
}

func (s *exchangeOutput) finish(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err == nil {
		if err == nil {
			err = io.EOF
		}
		s.err = err
	}
	s.ready.Broadcast()
}
