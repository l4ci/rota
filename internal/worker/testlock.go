package worker

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/shlex"
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
		slot, release, err := tryTestSlot(paths)
		if err != nil {
			return err
		}
		if release != nil {
			defer release()
			os.WriteFile(slot, fmt.Appendf(nil, "%d %s", os.Getpid(), verb), 0o644)
			defer os.Truncate(slot, 0)
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

// tryTestSlot locks the first free slot file without blocking and returns its
// path and the release; a nil release when every slot is held.
func tryTestSlot(paths []string) (string, func(), error) {
	for _, p := range paths {
		release, ok, err := fsio.TryLock(p)
		if err != nil {
			return "", nil, err
		}
		if ok {
			return p, release, nil
		}
	}
	return "", nil, nil
}

// The holder record is advisory: a holder killed mid-run leaves its pid in the
// slot file until the next holder overwrites it; the flock is the truth.

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
	return prefix + "sh -c " + shlex.QuoteAlways(command), true
}

// maxDefaultShards caps the machine-sized smoke shard count.
const maxDefaultShards = 4

// DefaultSmokeShards is the smoke shard count test/gate.sh picks when nothing
// overrides it: one shard per two cores, at least one, at most four. The shell
// computes the same from nproc.
func DefaultSmokeShards(cpus int) int {
	return max(1, min(cpus/2, maxDefaultShards))
}

// SmokeShards is the shard count test/gate.sh would use and where it came
// from, in the gate's override order: ROTA_SMOKE_SHARDS (envVal), else
// DefaultSmokeShards. A value the gate would reject falls through.
func SmokeShards(envVal string, cpus int) (int, string) {
	if n, err := strconv.Atoi(envVal); err == nil && n >= 1 {
		return n, "ROTA_SMOKE_SHARDS"
	}
	return DefaultSmokeShards(cpus), fmt.Sprintf("default for %d cores", cpus)
}
