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
