package worker

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
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
// frees or ctx ends. Waiters are served in arrival order: a ticket queue keeps
// a later arrival from taking a slot freed while an earlier waiter is queued. The land lock is separate and taken independently.
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
	q, err := joinTestQueue(ctx, rotastate.File(common, "test-queue"))
	if err != nil {
		return err
	}
	defer q.leave()
	told := false
	for {
		slot, release, err := q.tryTurn(ctx, paths)
		if err != nil {
			return err
		}
		if release != nil {
			q.leave() // a held slot is not a place in line: width > 1 waiters behind us go on
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

// testQueueGuardWait bounds the wait for the queue's short guard lock, held
// only for a directory scan.
const testQueueGuardWait = 30 * time.Second

// testQueue is a waiter's place in the arrival-ordered line for the test lock.
// Each waiter owns a ticket file <dir>/<seq> and holds a flock on it for as
// long as it waits, so a killed or cancelled waiter's ticket is detectable
// (its flock is free) and dropped by the next waiter that scans. Tickets are
// numbered under the guard lock <dir>.lock, so arrival order is a strict
// sequence, and only the lowest live ticket may take a freed slot.
type testQueue struct {
	dir     string
	name    string
	release func()
	once    sync.Once
}

// joinTestQueue takes the next ticket in dir.
func joinTestQueue(ctx context.Context, dir string) (*testQueue, error) {
	q := &testQueue{dir: dir}
	err := fsio.LockedCtx(ctx, dir, testQueueGuardWait, func() error {
		if err := os.MkdirAll(dir, 0o777); err != nil {
			return err
		}
		seqs, err := ticketSeqs(dir)
		if err != nil {
			return err
		}
		next := 1
		if len(seqs) > 0 {
			next = seqs[len(seqs)-1] + 1
		}
		q.name = fmt.Sprintf("%012d", next)
		rel, ok, err := fsio.TryLock(filepath.Join(dir, q.name))
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("test queue ticket %s is already locked", q.name)
		}
		q.release = rel
		return nil
	})
	if err != nil {
		return nil, err
	}
	return q, nil
}

// leave gives up the ticket: the file goes first, then the flock.
func (q *testQueue) leave() {
	q.once.Do(func() {
		_ = fsio.LockedCtx(context.Background(), q.dir, testQueueGuardWait, func() error {
			os.Remove(filepath.Join(q.dir, q.name))
			return nil
		})
		q.release()
	})
}

// tryTurn takes a free slot only when no live ticket precedes this one; tickets
// whose waiter is gone are removed on the way. A nil release means keep waiting.
func (q *testQueue) tryTurn(ctx context.Context, paths []string) (string, func(), error) {
	var slot string
	var release func()
	err := fsio.LockedCtx(ctx, q.dir, testQueueGuardWait, func() error {
		seqs, err := ticketSeqs(q.dir)
		if err != nil {
			return err
		}
		for _, n := range seqs {
			name := fmt.Sprintf("%012d", n)
			if name >= q.name {
				break
			}
			p := filepath.Join(q.dir, name)
			rel, ok, err := fsio.TryLock(p)
			if err != nil {
				return err
			}
			if !ok {
				return nil // a live waiter is ahead
			}
			os.Remove(p) // its waiter died: drop the ticket
			rel()
		}
		slot, release, err = tryTestSlot(paths)
		return err
	})
	return slot, release, err
}

// ticketSeqs lists the ticket numbers in dir, ascending.
func ticketSeqs(dir string) ([]int, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var seqs []int
	for _, e := range ents {
		if n, err := strconv.Atoi(e.Name()); err == nil {
			seqs = append(seqs, n)
		}
	}
	sort.Ints(seqs)
	return seqs, nil
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
