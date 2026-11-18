package fsio

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

// A context observes the first wait, so replacement happens after LockedCtx
// opened the original inode and found its flock busy, without timing a sleep.
type waitingContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *waitingContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestLockedRejectsReplacedSidecar(t *testing.T) {
	for _, replacement := range []string{"missing", "busy"} {
		t.Run(replacement, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			old, err := os.OpenFile(LockPath(path), os.O_RDWR|os.O_CREATE, 0o644)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			if err := syscall.Flock(int(old.Fd()), syscall.LOCK_EX); err != nil {
				t.Fatal(err)
			}
			ctx := &waitingContext{Context: context.Background(), waiting: make(chan struct{})}
			entered := false
			done := make(chan error, 1)
			go func() {
				done <- LockedCtx(ctx, path, 300*time.Millisecond, func() error {
					entered = true
					return nil
				})
			}()
			select {
			case <-ctx.waiting:
			case <-time.After(5 * time.Second):
				t.Fatal("waiter did not reach busy lock")
			}
			if err := os.Remove(LockPath(path)); err != nil {
				t.Fatal(err)
			}
			if replacement == "busy" {
				release, ok, err := TryLock(LockPath(path))
				if err != nil || !ok {
					t.Fatalf("replacement lock: ok=%v err=%v", ok, err)
				}
				defer release()
			}
			if err := syscall.Flock(int(old.Fd()), syscall.LOCK_UN); err != nil {
				t.Fatal(err)
			}
			err = <-done
			if replacement == "busy" {
				if entered || !errors.Is(err, ErrLockTimeout) {
					t.Fatalf("waiter entered replaced lock: entered=%v err=%v", entered, err)
				}
			} else {
				if err != nil || !entered {
					t.Fatalf("retry missing sidecar: entered=%v err=%v", entered, err)
				}
				if _, err := os.Stat(LockPath(path)); err != nil {
					t.Fatalf("waiter used unlinked inode: %v", err)
				}
			}
		})
	}
}
