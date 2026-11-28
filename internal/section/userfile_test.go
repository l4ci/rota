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

// A link a repository carries must not send the write outside the project or
// into .git: the block holds text the repository chose.
func TestUpsertBlockRefusesLinkOutOfProject(t *testing.T) {
	for name, dest := range map[string]string{"outside": "", "git dir": ".git/hooks/pre-commit"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(t.TempDir(), "bashrc")
			if dest != "" {
				target = filepath.Join(dir, filepath.FromSlash(dest))
				if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(target, []byte("mine\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(dir, "AGENTS.md")
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			if _, err := UpsertManaged(dir, "x", "body"); err == nil {
				t.Error("wrote through the link")
			}
			if raw, _ := os.ReadFile(target); string(raw) != "mine\n" {
				t.Errorf("target changed: %q", raw)
			}
			if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
				t.Errorf("link replaced: %v", err)
			}
		})
	}
}
