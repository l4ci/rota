package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/rotastate"
)

// testLockPoll is how often a waiter retries every slot.
const testLockPoll = 100 * time.Millisecond

// WithTestLock runs fn holding one of width test slots: the files
// <git-common-dir>/rota/test-<n>.lock, shared by every worktree of the repo,
// each a flock the kernel drops with its holder. Heavy test runs (the gate,
// `rota test run`, `rota proof record`) queue here so several of them do not
// starve each other of CPU. A busy lock is never a failure and has no timeout:
// the waiter says so once on stderr, naming the holder, and polls until a slot
// frees or ctx ends. The land lock is separate and taken independently.
func (e Env) WithTestLock(ctx context.Context, root string, width int, verb string, stderr io.Writer, fn func() error) error {
	common, err := rotastate.CommonDirVia(ctx, e.WithDefaults().Git, root)
	if err != nil {
		return err
	}
	if width < 1 {
		width = 1
	}
	if err := os.MkdirAll(rotastate.Dir(common), 0o777); err != nil {
		return err
	}
	paths := make([]string, width)
	for i := range paths {
		paths[i] = rotastate.File(common, fmt.Sprintf("test-%d.lock", i))
	}
	told := false
	for {
		f, err := tryTestSlot(paths)
		if err != nil {
			return err
		}
		if f != nil {
			defer f.Close() // closing the fd releases the flock
			f.Truncate(0)
			fmt.Fprintf(f, "%d %s", os.Getpid(), verb)
			defer f.Truncate(0)
			return fn()
		}
		if !told {
			told = true
			fmt.Fprintf(stderr, "waiting for the test lock (held by %s)\n", testLockHolder(paths))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(testLockPoll):
		}
	}
}

// tryTestSlot flocks the first free slot file without blocking; nil, nil when
// every slot is held.
func tryTestSlot(paths []string) (*os.File, error) {
	for _, p := range paths {
		f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0o644)
		if err != nil {
			return nil, err
		}
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return f, nil
		}
		f.Close()
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			return nil, err
		}
	}
	return nil, nil
}

// testLockHolder is "<pid> <verb>" of a holder, from the first slot that
// names one; "unknown" when a holder has not written its record yet.
func testLockHolder(paths []string) string {
	for _, p := range paths {
		if b, err := os.ReadFile(p); err == nil && strings.TrimSpace(string(b)) != "" {
			return strings.TrimSpace(string(b))
		}
	}
	return "unknown"
}

// NiceCommand wraps a shell command so it runs at lower priority: nice -n 10,
// and ionice -c3 when ionice is on PATH. lookPath is exec.LookPath, passed in
// for tests. A host without nice gets the command unchanged. The second result
// says whether it was wrapped.
func NiceCommand(command string, lookPath func(string) (string, error)) (string, bool) {
	if _, err := lookPath("nice"); err != nil {
		return command, false
	}
	prefix := "nice -n 10 "
	if _, err := lookPath("ionice"); err == nil {
		prefix += "ionice -c3 "
	}
	return prefix + "sh -c " + shellQuote(command), true
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// maxDefaultShards caps the machine-sized smoke shard count.
const maxDefaultShards = 4

// DefaultSmokeShards is the smoke shard count test/gate.sh picks when nothing
// overrides it: one shard per two cores, at least one, at most four. The shell
// computes the same from nproc.
func DefaultSmokeShards(cpus int) int {
	return max(1, min(cpus/2, maxDefaultShards))
}

// SmokeShards is the shard count test/gate.sh would use and where it came
// from, in the gate's override order: ROTA_SMOKE_SHARDS (envVal), then
// gate.smokeShards when the config sets it, else DefaultSmokeShards. A value
// the gate would reject falls through to the next source.
func SmokeShards(cfg any, envVal string, cpus int) (int, string) {
	if n, err := strconv.Atoi(envVal); err == nil && n >= 1 {
		return n, "ROTA_SMOKE_SHARDS"
	}
	if _, ok := config.Lookup(cfg, "gate.smokeShards"); ok {
		if n, err := config.Int(cfg, "gate.smokeShards", 1, 1<<20); err == nil {
			return n, "gate.smokeShards"
		}
	}
	return DefaultSmokeShards(cpus), fmt.Sprintf("default for %d cores", cpus)
}
