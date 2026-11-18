package fsio

import (
	"os"
	"path"
	"path/filepath"
)

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

// GlobDir lists dir's entries whose base name matches pattern, as full paths in
// name order. The pattern applies to the base name only, so a dir containing
// [, * or ? still matches (filepath.Glob would read them as pattern syntax).
// A missing or unreadable dir, or a malformed pattern, is an empty list.
func GlobDir(dir, pattern string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if ok, err := path.Match(pattern, e.Name()); err == nil && ok {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}
