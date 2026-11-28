package fsio

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// maxLinkHops bounds the walk along a dangling symlink chain.
const maxLinkHops = 40

// ErrLinkDirMissing is wrapped by the refusal to write through a symlink whose
// target directory does not exist.
var ErrLinkDirMissing = errors.New("symlink target directory does not exist")

// WriteUserFile atomically rewrites a file the user owns (instruction files,
// harness settings). A symlink stays in place and its target is written,
// wherever it points, and an existing file keeps its permission bits; a new
// file is created as WriteFileAtomic creates it. A link into a missing
// directory is refused with ErrLinkDirMissing. State rota owns keeps
// WriteFileAtomic, and paths that refuse symlinks must check before they call
// either (SymlinkIn).
func WriteUserFile(path string, data []byte) error {
	target, err := linkTarget(path)
	if err != nil {
		return err
	}
	if target != path {
		if _, err := os.Stat(filepath.Dir(target)); errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %s links to %s; create the directory or remove the link", ErrLinkDirMissing, path, target)
		}
	}
	fi, err := os.Stat(target)
	if errors.Is(err, os.ErrNotExist) {
		return WriteFileAtomic(target, data)
	}
	if err != nil {
		return err
	}
	return WriteFileAtomicMode(target, data, fi.Mode().Perm())
}

// linkTarget is the file path leads to: path itself unless it is a symlink. A
// dangling link yields the missing file it points at; a loop is an error.
func linkTarget(path string) (string, error) {
	for range maxLinkHops {
		fi, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			return path, nil
		}
		if err != nil {
			return "", err
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			return path, nil
		}
		next, err := os.Readlink(path)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(next) {
			// The link is relative to the directory it really sits in, which
			// differs from the spelled one when a parent is a link too.
			dir, err := filepath.EvalSymlinks(filepath.Dir(path))
			if errors.Is(err, os.ErrNotExist) {
				dir = filepath.Dir(path)
			} else if err != nil {
				return "", err
			}
			next = filepath.Join(dir, next)
		}
		path = next
	}
	return "", &os.PathError{Op: "readlink", Path: path, Err: syscall.ELOOP}
}
