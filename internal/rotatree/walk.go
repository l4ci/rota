package rotatree

import "path/filepath"

// Ascend calls visit with start, then each parent directory up to and
// including the filesystem root, and stops as soon as visit returns true. It
// reports whether visit stopped the walk. It is the one upward walk to a root
// marker: callers pass the predicate for their marker and keep their own
// reading of the result.
func Ascend(start string, visit func(dir string) bool) bool {
	dir := start
	for {
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
