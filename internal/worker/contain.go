package worker

import (
	"path/filepath"
	"strings"
)

// PathContains compares physical paths, with component boundaries (wt-2 is
// not inside wt). Relative paths are resolved against the process cwd; an
// empty path is unset, never the cwd, so it contains and is contained by nothing.
func PathContains(parent, child string) bool {
	if parent == "" || child == "" {
		return false
	}
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

// Holder is a round slot and the worktree it holds, as recorded.
type Holder struct{ Name, Worktree string }

// HeldBy names the holder whose worktree is wt or contains it, "" when none.
// It is the one rule for "a round slot owns this worktree", shared by ship's
// clearing and reap. A relative Worktree resolves against root; a holder with
// no recorded Worktree holds nothing.
func HeldBy(root string, holders []Holder, wt string) string {
	for _, h := range holders {
		if h.Worktree == "" {
			continue
		}
		path := h.Worktree
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		if PathContains(path, wt) {
			return h.Name
		}
	}
	return ""
}
