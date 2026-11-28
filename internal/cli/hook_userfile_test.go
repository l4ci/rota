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

// The user's own link is followed wherever it points (#4438).
func TestUserFileLinkOutOfProjectIsWritten(t *testing.T) {
	dir := orchProject(t, false)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, link := range map[string]string{"settings.json": ".claude/settings.json", "CLAUDE.md": "CLAUDE.md"} {
		target := filepath.Join(outside, name)
		if err := os.WriteFile(target, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		link = filepath.Join(dir, filepath.FromSlash(link))
		os.Remove(link)
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	os.Remove(filepath.Join(dir, "AGENTS.md"))
	for name, args := range map[string][]string{
		"settings.json": {"hook", "install", "--scope", "project", "--json"},
		"CLAUDE.md":     {"block", "knowledge", "--json"},
	} {
		if code, out, errOut := rotaIn(t, dir, args...); code != 0 {
			t.Errorf("%s: exit %d: %s %s", name, code, out, errOut)
		}
		if raw, _ := os.ReadFile(filepath.Join(outside, name)); string(raw) == "{}\n" {
			t.Errorf("%s: target not written", name)
		}
	}
}

// A link into a directory that does not exist is refused with exit 5, naming
// the link and its target.
func TestUserFileLinkIntoMissingDirIsRefused(t *testing.T) {
	for name, tc := range map[string]struct {
		link string
		args []string
	}{
		"settings": {"cfg/settings.json", []string{"hook", "install", "--scope", "user", "--json"}},
		"block":    {"CLAUDE.md", []string{"block", "knowledge", "--json"}},
	} {
		t.Run(name, func(t *testing.T) {
			dir := orchProject(t, false)
			t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, "cfg"))
			link := filepath.Join(dir, filepath.FromSlash(tc.link))
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatal(err)
			}
			os.Remove(filepath.Join(dir, "AGENTS.md"))
			os.Remove(link)
			target := filepath.Join(t.TempDir(), "gone", "file")
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			code, out, errOut := rotaIn(t, dir, tc.args...)
			all := out + errOut
			if code != ExitUnavailable {
				t.Errorf("exit %d, want %d: %s", code, ExitUnavailable, all)
			}
			if !strings.Contains(all, filepath.Base(link)) || !strings.Contains(all, target) || strings.Contains(all, "state file unreadable") {
				t.Errorf("message must name the link and target: %s", all)
			}
			if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
				t.Errorf("link replaced: %v", err)
			}
		})
	}
}
