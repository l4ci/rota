package section

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A managed-block write on the user's instructions file follows a symlink and
// keeps the mode (#4438).
func TestUpsertBlockWritesThroughSymlinkAndKeepsMode(t *testing.T) {
	dir := t.TempDir()
	claude, agents := filepath.Join(dir, "CLAUDE.md"), filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(claude, []byte("# Project\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("CLAUDE.md", agents); err != nil {
		t.Fatal(err)
	}
	if _, err := UpsertManaged(dir, "x", "body"); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(agents); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("AGENTS.md is no longer a symlink: %v %v", fi.Mode(), err)
	}
	raw, _ := os.ReadFile(claude)
	if !strings.Contains(string(raw), "body") {
		t.Errorf("target not updated: %q", raw)
	}
	if fi, _ := os.Stat(claude); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 600", fi.Mode().Perm())
	}
}
