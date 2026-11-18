package fsio

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/l4ci/rota/internal/jsonx"
)

// LockTimeout and lockPoll match hvlib_io.locked.
const (
	LockTimeout = 10 * time.Second
	lockPoll    = 50 * time.Millisecond
)

// ErrLockTimeout is wrapped by the error Locked returns when the lock stays busy.
var ErrLockTimeout = errors.New("lock timeout")

// lockSuffix names a data file's lock sidecar: "<path>" + lockSuffix.
const lockSuffix = ".lock"

// LockPath is the lock sidecar Locked takes for path.
func LockPath(path string) string { return path + lockSuffix }

// Locked runs fn while holding an exclusive flock on "<path>.lock". The lock
// sits on a sibling file because the atomic rename swaps the data file's
// inode. A busy lock is retried every 50 ms until timeout. The kernel drops
// the lock if the process dies; the empty .lock file stays in place.
// Acquisition retries if the sidecar was replaced after opening it. Callers
// must keep it in place while holding the lock, and use a consistent order
// when taking multiple locks.
func Locked(path string, timeout time.Duration, fn func() error) error {
	return LockedCtx(context.Background(), path, timeout, fn)
}

// LockedCtx is Locked that also stops waiting when ctx ends, returning
// ctx.Err() without running fn. A held lock is never interrupted: ctx only
// bounds the wait.
func LockedCtx(ctx context.Context, path string, timeout time.Duration, fn func() error) error {
	lockPath := path + lockSuffix
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o777); err != nil {
		return err
	}
	var f *os.File
	defer func() {
		if f != nil {
			f.Close() // closing the fd releases the flock
		}
	}()
	deadline := time.Now().Add(timeout)
	for {
		if f == nil {
			var err error
			f, err = os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o644)
			if err != nil {
				return err
			}
		}
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			current, err := lockFileCurrent(f, lockPath)
			if err != nil {
				return err
			}
			if current {
				break
			}
			// A waiter may have opened a sidecar that was since removed or
			// replaced. Release that inode and reopen the current path.
			f.Close()
			f = nil
		} else if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			return err
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("timed out after %ss waiting for lock %s: %w",
				jsonx.PyFloat(timeout.Seconds()), lockPath, ErrLockTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(lockPoll):
		}
	}
	return fn()
}

// lockFileCurrent rejects a flock on an inode no longer named by path.
// Callers must still keep sidecars in place throughout their critical section.
func lockFileCurrent(f *os.File, path string) (bool, error) {
	opened, err := f.Stat()
	if err != nil {
		return false, err
	}
	current, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return os.SameFile(opened, current), nil
}

// RemoveWithLock removes path and its lock sidecar. Use it only where no
// concurrent locker is expected: removing a held sidecar lets a newcomer
// acquire another inode before the holder finishes. The acquisition inode
// check fences waiters on the old file, not an already running callback.
// A missing sidecar is not an error; the result is path's own Remove error.
func RemoveWithLock(path string) error {
	os.Remove(path + lockSuffix)
	return os.Remove(path)
}

// RemoveSidecars removes the lock sidecar of every file in dir whose name
// matches dataPattern (path.Match syntax). Errors are ignored: a sidecar is
// an empty marker and a missing one is the goal.
func RemoveSidecars(dir, dataPattern string) {
	for _, c := range GlobDir(dir, dataPattern+lockSuffix) {
		os.Remove(c)
	}
}

// HeldLockUnder walks root for lock sidecars and returns the first one some
// process holds, or "" when none is. It creates no file. skip, when non-nil,
// is asked about every directory and prunes it when it returns true.
func HeldLockUnder(root string, skip func(path string, d fs.DirEntry) bool) string {
	var held string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || held != "" {
			return nil
		}
		if d.IsDir() {
			if path != root && skip != nil && skip(path, d) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, lockSuffix) {
			return nil
		}
		f, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			return nil
		}
		defer f.Close()
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			if errors.Is(err, syscall.EWOULDBLOCK) {
				held = path
			}
			return nil
		}
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		return nil
	})
	return held
}

// TryLock takes an exclusive flock on path without blocking, creating the file
// when missing. ok is false (and release nil) when another handle holds it. On
// success the lock lasts until release is called or the process dies; release
// is idempotent. Unlike Locked, the lock sits on path itself, so use it for a
// file that is only a lock, never an atomically replaced data file.
func TryLock(path string) (release func(), ok bool, err error) {
	var f *os.File
	for {
		var err error
		f, err = os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
		if err != nil {
			return nil, false, err
		}
		for {
			err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
			if !errors.Is(err, syscall.EINTR) {
				break
			}
		}
		if err != nil {
			f.Close()
			if errors.Is(err, syscall.EWOULDBLOCK) {
				return nil, false, nil
			}
			return nil, false, err
		}
		current, err := lockFileCurrent(f, path)
		if err != nil {
			f.Close()
			return nil, false, err
		}
		if current {
			break
		}
		f.Close()
	}
	var once sync.Once
	return func() { once.Do(func() { f.Close() }) }, true, nil // closing the fd releases the flock
}
