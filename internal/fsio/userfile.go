package fsio

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// maxLinkHops bounds the walk along a dangling symlink chain.
const maxLinkHops = 40

// ErrLinkOutside is wrapped by the refusal to follow a repository's symlink
// out of the repository or into its .git.
var ErrLinkOutside = errors.New("symlink leaves the project")

// WriteUserFile atomically rewrites a file the user owns (instruction files,
// harness settings). A symlink stays in place and its target is written, and an
// existing file keeps its permission bits; a new file is created as
// WriteFileAtomic creates it. State rota owns keeps WriteFileAtomic, and paths
// that refuse symlinks must check before they call either (SymlinkIn).
//
// root bounds a link a repository can carry: when path is a symlink, its target
// must lie under root and outside root's .git, else the error wraps
// ErrLinkOutside and ErrUnreadable. Pass "" for a file in the user's own
// config, which may link anywhere.
func WriteUserFile(root, path string, data []byte) error {
	target, err := linkTarget(path)
	if err != nil {
		return err
	}
	if root != "" && target != path {
		if target, err = confine(root, path, target); err != nil {
			return err
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

// confine resolves the directory of target, which path links to, and refuses a
// result outside root or inside root's .git. It returns the resolved target.
func confine(root, path, target string) (string, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(target))
	if err != nil {
		return "", err
	}
	target = filepath.Join(dir, filepath.Base(target))
	rel, err := filepath.Rel(realRoot, target)
	first, _, _ := strings.Cut(filepath.ToSlash(rel), "/")
	if err != nil || !filepath.IsLocal(rel) || strings.EqualFold(first, ".git") {
		return "", Unreadable(fmt.Errorf("%w: %s links to %s, rota will not write there", ErrLinkOutside, path, target))
	}
	return target, nil
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
			if err != nil {
				return "", err
			}
			next = filepath.Join(dir, next)
		}
		path = next
	}
	return "", &os.PathError{Op: "readlink", Path: path, Err: syscall.ELOOP}
}
