package testiso

import (
	"io/fs"
	"os"
	"path/filepath"
)

// NestedCheckout reports whether path is a directory below root that holds its
// own .git (file or dir): a linked worktree or another clone sitting inside this
// checkout, such as .claude/worktrees/agent-<id>. Source-scanning tests call it
// from their WalkDir callback and return fs.SkipDir on true, so they cover only
// this checkout's sources whatever is nested in it.
func NestedCheckout(root, path string, d fs.DirEntry) bool {
	if !d.IsDir() || path == root {
		return false
	}
	_, err := os.Lstat(filepath.Join(path, ".git"))
	return err == nil
}
