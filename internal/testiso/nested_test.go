package testiso

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNestedCheckoutSkipsPlantedWorktree(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".git", "gitdir: elsewhere\n") // the root itself is a checkout and is not skipped
	write("own/a.go", "FORBIDDEN")
	write(".claude/worktrees/agent-1/.git", "gitdir: ../../../.git/worktrees/agent-1\n") // worktree: .git file
	write(".claude/worktrees/agent-1/b.go", "FORBIDDEN")
	write("clones/c/.git/HEAD", "ref: refs/heads/main\n") // clone: .git dir
	write("clones/c/c.go", "FORBIDDEN")

	var seen []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if NestedCheckout(root, path, d) {
			return fs.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(path, ".go") {
			rel, _ := filepath.Rel(root, path)
			seen = append(seen, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0] != "own/a.go" {
		t.Fatalf("walk saw %v, want only own/a.go", seen)
	}
}
