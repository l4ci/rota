package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hook install and uninstall rewrite a symlinked settings file through the
// link and keep its mode (#4438).
func TestHookInstallKeepsSymlinkedSettings(t *testing.T) {
	dir := orchProject(t, false)
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	target := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(target, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(cfg, "settings.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	for _, verb := range []string{"install", "uninstall"} {
		before, _ := os.ReadFile(target)
		if code, out, errOut := rotaIn(t, dir, "hook", verb, "--scope", "user", "--json"); code != 0 {
			t.Fatalf("%s: %d %s %s", verb, code, out, errOut)
		}
		if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Errorf("%s: settings.json is no longer a symlink: %v", verb, err)
		}
		after, _ := os.ReadFile(target)
		if string(after) == string(before) {
			t.Errorf("%s: target not updated: %s", verb, strings.TrimSpace(string(after)))
		}
		if fi, _ := os.Stat(target); fi.Mode().Perm() != 0o600 {
			t.Errorf("%s: mode = %o, want 600", verb, fi.Mode().Perm())
		}
	}
}

// A project settings file linked out of the project is refused, not written.
func TestHookInstallRefusesProjectSettingsLinkedOutside(t *testing.T) {
	dir := orchProject(t, false)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	target := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(target, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, ".claude", "settings.json")); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := rotaIn(t, dir, "hook", "install", "--scope", "project", "--json")
	if code != ExitResolution {
		t.Errorf("exit %d, want %d: %s %s", code, ExitResolution, out, errOut)
	}
	if raw, _ := os.ReadFile(target); string(raw) != "{}\n" {
		t.Errorf("target changed: %s", raw)
	}
}
