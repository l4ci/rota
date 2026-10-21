package worker

import (
	"path/filepath"
	"strings"
)

// PathContains compares physical paths, with component boundaries (wt-2 is
// not inside wt). Relative paths are resolved against the process cwd.
func PathContains(parent, child string) bool {
	clean := func(p string) string {
		if real, err := filepath.EvalSymlinks(p); err == nil {
			p = real
		}
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		return filepath.Clean(p)
	}
	rel, err := filepath.Rel(clean(parent), clean(child))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
