package testiso

import (
	"io/fs"
	"os"
	"path/filepath"
)

// ProjectTemp reports whether path is the project temp root (<root>/.rota/tmp,
// #2499): rota's scratch space, never source. Source-scanning tests return
// fs.SkipDir on true so a stray scratch file cannot fail them. The names are
// spelled out (rotatree.DirName, rotatree.TmpDir): testiso stays import-free,
// since test/fast_tree_scans_test.py builds it into a minimal module.
func ProjectTemp(root, path string, d fs.DirEntry) bool {
	return d.IsDir() && path == filepath.Join(root, ".rota", "tmp")
}

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
