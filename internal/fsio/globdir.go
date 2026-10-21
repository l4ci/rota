package fsio

import (
	"os"
	"path"
	"path/filepath"
)

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
