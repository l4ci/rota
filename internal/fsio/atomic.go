package fsio

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
)

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

// rename is a seam for tests.
var rename = os.Rename

// MoveFile moves src to dst and leaves dst with mode perm. It renames; when
// that fails (typically across filesystems) it copies through
// WriteFileAtomicMode and removes src. A failed copy leaves src in place. src
// must be a regular file: a symlink would be moved as the link, which is the
// caller's call to make.
func MoveFile(src, dst string, perm os.FileMode) error {
	if rename(src, dst) == nil {
		return os.Chmod(dst, perm)
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := WriteFileAtomicMode(dst, b, perm); err != nil {
		return err
	}
	return os.Remove(src)
}

// MoveFilePreserveMode is MoveFile with the mode taken from src instead of a
// caller-supplied one: rename keeps it as is, and the cross-filesystem copy
// applies src's permission bits to dst before src is removed. A failed copy
// leaves src in place.
func MoveFilePreserveMode(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if rename(src, dst) == nil {
		return nil
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := WriteFileAtomicMode(dst, b, info.Mode().Perm()); err != nil {
		return err
	}
	return os.Remove(src)
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
