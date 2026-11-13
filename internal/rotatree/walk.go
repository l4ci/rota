package rotatree

import "path/filepath"

// Ascend calls visit with start, then each parent directory up to and
// including the filesystem root, and stops as soon as visit returns true. It
// reports whether visit stopped the walk. It is the one upward walk to a root
// marker: callers pass the predicate for their marker and keep their own
// reading of the result.
//
// The walk ends, unvisited, at a project temp root (IsTempRoot): a review tree
// or scratch dir made there is no part of the project, so a rota verb run in
// it never resolves, reads or writes the live .rota/ above (#2499).
func Ascend(start string, visit func(dir string) bool) bool {
	dir := start
	for {
		if IsTempRoot(dir) {
			return false
		}
		if visit(dir) {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// Find is the nearest directory at or above start that holds .rota/.
func Find(start string) (string, bool) {
	var root string
	ok := Ascend(start, func(dir string) bool {
		root = dir
		return Exists(dir)
	})
	if !ok {
		return "", false
	}
	return root, true
}

// IsTempRoot reports whether dir is, by name, a project temp root: a TmpDir
// directly under a .rota/ directory. It reads no filesystem.
func IsTempRoot(dir string) bool {
	return filepath.Base(dir) == TmpDir && filepath.Base(filepath.Dir(dir)) == DirName
}
