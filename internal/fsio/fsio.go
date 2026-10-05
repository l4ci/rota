// Package fsio holds rota's file primitives: atomic writes and the sidecar
// advisory lock. Both match bin/hvlib_io.py, so rota and the old helpers can
// share state files during the port (docs/design/5.0-cli-conventions.md, Writes).
package fsio

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// LoadJSON reads a JSON document, or returns def when the file is missing
// or does not parse, like hvlib_io.load_json.
func LoadJSON(path string, def any) any {
	raw, err := os.ReadFile(path)
	if err != nil {
		return def
	}
	v, err := jsonx.Decode(raw)
	if err != nil {
		return def
	}
	return v
}

// ReadText reads a text file the way Python's Path.read_text does: universal
// newlines, so "\r\n" and a lone "\r" both become "\n". A missing file
// returns the os error. Use it for every markdown or text state file that is
// parsed or rewritten, so a CRLF file does not end up with mixed endings.
func ReadText(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	t := strings.ReplaceAll(string(b), "\r\n", "\n")
	return strings.ReplaceAll(t, "\r", "\n"), nil
}

// WriteFileAtomic writes data to "<path>.tmp" in the same directory and
// renames it over path, so readers see the old or the new file, never half.
// Unlike hvlib_io it also fsyncs the file before the rename and the
// directory after it, so a crash cannot leave an empty or missing file.
func WriteFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o666)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return syncDir(filepath.Dir(path))
}

// syncDir makes a rename in dir durable. It runs after the rename has
// succeeded, so a filesystem that does not support fsync on a directory
// (EINVAL, ENOTSUP) must not turn a completed write into a reported failure.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	if err := dirSync(d); err != nil && !errors.Is(err, syscall.EINVAL) && !errors.Is(err, syscall.ENOTSUP) {
		return err
	}
	return nil
}

// dirSync is a seam for tests.
var dirSync = func(d *os.File) error { return d.Sync() }

// WriteJSONAtomic writes v as json.dumps(v, indent=2) plus a newline.
func WriteJSONAtomic(path string, v any) error {
	data, err := jsonx.Marshal(v)
	if err != nil {
		return err
	}
	return WriteFileAtomic(path, append(data, '\n'))
}

// Locked runs fn while holding an exclusive flock on "<path>.lock". The lock
// sits on a sibling file because the atomic rename swaps the data file's
// inode. A busy lock is retried every 50 ms until timeout. The kernel drops
// the lock if the process dies; the empty .lock file stays in place.
// Hold at most one lock at a time.
func Locked(path string, timeout time.Duration, fn func() error) error {
	lockPath := path + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o777); err != nil {
		return err
	}
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer f.Close() // closing the fd releases the flock
	deadline := time.Now().Add(timeout)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			return err
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("timed out after %ss waiting for lock %s: %w",
				jsonx.PyFloat(timeout.Seconds()), lockPath, ErrLockTimeout)
		}
		time.Sleep(lockPoll)
	}
	return fn()
}

// UpdateJSON is a locked read-modify-write: it loads path (def when missing
// or corrupt), lets mutate return the new value, and writes it atomically.
func UpdateJSON(path string, def any, mutate func(any) (any, error)) error {
	return updateJSON(path, def, LockTimeout, mutate)
}

// updateJSON is UpdateJSON with an explicit lock budget, so a test can give a
// heavily contended lock more time than production allows.
func updateJSON(path string, def any, timeout time.Duration, mutate func(any) (any, error)) error {
	return Locked(path, timeout, func() error {
		v, err := mutate(LoadJSON(path, def))
		if err != nil {
			return err
		}
		return WriteJSONAtomic(path, v)
	})
}
