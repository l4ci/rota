package cli

import (
	"context"
	"flag"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/worker"
)

// testLockHeldEnv marks a process that runs inside a held test lock. A rota
// verb started under one (the gate's go test sections, the `rota test run fast`
// a proof record wraps) reads it and does not queue behind its own parent.
const testLockHeldEnv = "ROTA_TEST_LOCK_HELD"

// withTestLock runs fn holding a slot of the repository's test lock (width
// test.slots), unless an enclosing run already holds one. A busy lock waits and
// says so on stderr; it never fails.
func withTestLock(c *Ctx, ctx context.Context, root string, gc worker.GateConfig, verb string, fn func() error) error {
	if c.deps().Getenv(testLockHeldEnv) != "" {
		return fn()
	}
	slots, err := gc.TestSlots()
	if err != nil {
		return Resolution("%v", err)
	}
	return workerEnvCtx(c, ctx).WithTestLock(ctx, root, slots, verb, c.Stderr, fn)
}

func (d *Deps) lookPath() func(string) (string, error) {
	if d.LookPath != nil {
		return d.LookPath
	}
	return exec.LookPath
}

// testLock is `rota test lock -- <command>...`: run the command holding a test
// slot. test/gate.sh runs itself through it, so the shell script needs no lock
// code. The command's exit status is the verb's.
func testLock(fs *flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		if c.dashAt != 0 || len(args) == 0 {
			return Result{}, Usage("usage: rota test lock -- <command> [<arg>...]").
				WithHint("the command goes after --, e.g. rota test lock -- bash test/gate.sh")
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		gc := c.deps().workerEnv().GateConfig(root)
		ctx, stop := workerContext()
		defer stop()
		code := 0
		err = withTestLock(c, ctx, root, gc, "test lock", func() error {
			cmd := exec.CommandContext(ctx, args[0], args[1:]...)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = c.Stdin, c.Stdout, c.Stderr
			cmd.Env = append(os.Environ(), testLockHeldEnv+"=1")
			// An interrupt ends the child the way a terminal would, so its own
			// traps clean up; the wait ends the process if it ignores that.
			cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
			cmd.WaitDelay = 30 * time.Second
			runErr := cmd.Run()
			if ee, ok := runErr.(*exec.ExitError); ok {
				code = ee.ExitCode()
				return nil
			}
			return runErr
		})
		if err != nil {
			return Result{}, err
		}
		if code != 0 {
			return Result{}, exitcode.Errf(code, "%s exited %d", args[0], code)
		}
		return Result{}, nil
	}
}
