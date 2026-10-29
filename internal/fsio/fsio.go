// Package fsio holds rota's file primitives: atomic writes and the sidecar
// advisory lock. Both match bin/hvlib_io.py, so rota and the old helpers can
// share state files during the port (docs/contributing/contract/cli-conventions.md, Writes).
package fsio

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/l4ci/rota/internal/pystr"

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
	return pystr.UniversalNewlines(string(b)), nil
}

// tmpSeq numbers temp files within one process; the pid separates processes.
var tmpSeq atomic.Uint64

// WriteFileAtomic writes data to a unique "<path>.<pid>.<n>.tmp" in the same
// directory and renames it over path, so readers see the old or the new file,
// never half. Unlike hvlib_io it also fsyncs the file before the rename and the
// directory after it, so a crash cannot leave an empty or missing file.
func WriteFileAtomic(path string, data []byte) error {
	return writeFileAtomic(path, data, 0o666, false)
}

// WriteFileAtomicMode is WriteFileAtomic with an explicit file mode. perm is
// applied to the temp file before the rename, exactly and not masked by the
// umask, so the result has that mode whether path was new or overwritten.
func WriteFileAtomicMode(path string, data []byte, perm os.FileMode) error {
	return writeFileAtomic(path, data, perm, true)
}

func writeFileAtomic(path string, data []byte, perm os.FileMode, exact bool) error {
	// A per-call temp name keeps two writers to one path from renaming each
	// other's file; the last rename wins with its own complete content.
	tmp := fmt.Sprintf("%s.%d.%d.tmp", path, os.Getpid(), tmpSeq.Add(1))
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if exact {
		err = f.Chmod(perm)
	}
	if err == nil {
		_, err = f.Write(data)
	}
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

// ErrNotLanded is wrapped by the error a verified write returns when the file
// read back is not what was written.
var ErrNotLanded = errors.New("write did not land")

// writeJSONVerified is WriteJSONAtomic plus a re-read: a success exit with
// nothing (or something else) on disk is an error, not a success (#579).
func writeJSONVerified(path string, v any) error {
	data, err := jsonx.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := WriteFileAtomic(path, data); err != nil {
		return err
	}
	got, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%w: %s unreadable after writing: %v", ErrNotLanded, path, err)
	}
	if !bytes.Equal(got, data) {
		return fmt.Errorf("%w: %s differs from what was written", ErrNotLanded, path)
	}
	return nil
}

// ErrUnreadable is wrapped by the error UpdateJSONStrict returns for a file
// that exists but cannot be read or parsed.
var ErrUnreadable = errors.New("state file unreadable")

// Locked runs fn while holding an exclusive flock on "<path>.lock". The lock
// sits on a sibling file because the atomic rename swaps the data file's
// inode. A busy lock is retried every 50 ms until timeout. The kernel drops
// the lock if the process dies; the empty .lock file stays in place.
// Callers taking multiple locks must use a consistent order across operations.
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

// WriteMarker writes v as indented JSON to path under the path's lock. guard,
// when non-nil, runs under the lock first and its error aborts the write.
func WriteMarker(path string, v any, guard func() error) error {
	b, err := jsonx.Marshal(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		return err
	}
	return Locked(path, LockTimeout, func() error {
		if guard != nil {
			if err := guard(); err != nil {
				return err
			}
		}
		return WriteFileAtomic(path, append(b, '\n'))
	})
}

// RemoveMarker deletes path under the path's lock when owned says the
// contents are the caller's, so a successor's marker is never removed. A
// missing file is not an error.
func RemoveMarker(path string, owned func(data []byte) bool) error {
	return Locked(path, LockTimeout, func() error {
		b, err := os.ReadFile(path)
		if err != nil || !owned(b) {
			return nil
		}
		return os.Remove(path)
	})
}

// UpdateJSON is a locked read-modify-write: it loads path (def when missing
// or corrupt), lets mutate return the new value, and writes it atomically.
func UpdateJSON(path string, def any, mutate func(any) (any, error)) error {
	return updateJSON(path, def, LockTimeout, mutate)
}

// UpdateJSONStrict is UpdateJSON for a file whose loss is destructive: a file
// that exists but does not parse is an error, where UpdateJSON would start
// from def and overwrite it. A missing file still starts from def. The write
// is re-read before it counts as done.
func UpdateJSONStrict(path string, def any, mutate func(any) (any, error)) error {
	return Locked(path, LockTimeout, func() error {
		cur := def
		raw, err := os.ReadFile(path)
		switch {
		case err == nil:
			if cur, err = jsonx.Decode(raw); err != nil {
				return fmt.Errorf("%w: %s does not parse (%v); fix or remove it, rota will not overwrite it", ErrUnreadable, path, err)
			}
		case !errors.Is(err, os.ErrNotExist):
			return fmt.Errorf("%w: %s: %v", ErrUnreadable, path, err)
		}
		v, err := mutate(cur)
		if err != nil {
			return err
		}
		return writeJSONVerified(path, v)
	})
}

// updateJSON is UpdateJSON with an explicit lock budget, so a test can give a
// heavily contended lock more time than production allows.
func updateJSON(path string, def any, timeout time.Duration, mutate func(any) (any, error)) error {
	return Locked(path, timeout, func() error {
		v, err := mutate(LoadJSON(path, def))
		if err != nil {
			return err
		}
		return writeJSONVerified(path, v)
	})
}

// IsDir reports whether p names an existing directory.
func IsDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// SameDir reports whether a and b name the same directory once symlinks are
// resolved on both sides. An empty path matches nothing. A side whose symlinks
// cannot be resolved (a path that does not exist) is compared cleaned but
// unresolved, so it matches only a path spelled the same way.
func SameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	norm := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			p = r
		}
		return filepath.Clean(p)
	}
	return norm(a) == norm(b)
}

// RemoveWithLock removes path and its lock sidecar. The sidecar goes first, so
// a caller inside Locked(path) drops both while still holding the lock. A
// missing sidecar is not an error; the result is path's own Remove error.
func RemoveWithLock(path string) error {
	os.Remove(path + ".lock")
	return os.Remove(path)
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
		if !strings.HasSuffix(path, ".lock") {
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
