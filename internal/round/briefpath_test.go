package round

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/roundcfg"
)

func writeAt(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// round.brief, then the project's skills/references/, then an installed Claude root
// (project before user). CLAUDE_PLUGIN_ROOT is no longer read.
func TestBriefPathOrder(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	cfgDir := ""
	env := func(k string) string {
		switch k {
		case "HOME":
			return home
		case "CLAUDE_CONFIG_DIR":
			return cfgDir
		case "CLAUDE_PLUGIN_ROOT":
			return filepath.Join(home, "plugin")
		}
		return ""
	}
	writeAt(t, filepath.Join(home, "plugin", "references", "worker-contract.md"), "plugin")
	if p, ok := briefPath(root, roundcfg.Settings{}, env); ok {
		t.Fatalf("CLAUDE_PLUGIN_ROOT still read: %s", p)
	}
	rel := filepath.Join("rota-orchestrate", "references", "worker-contract.md")
	userRoot := filepath.Join(home, ".claude", "skills")
	writeAt(t, filepath.Join(userRoot, rel), "user")
	if p, ok := briefPath(root, roundcfg.Settings{}, env); ok {
		t.Fatalf("a root without a manifest counts as installed: %s", p)
	}
	writeAt(t, filepath.Join(userRoot, ".rota-manifest.json"), "{}")
	if p, ok := briefPath(root, roundcfg.Settings{}, env); !ok || p != filepath.Join(userRoot, rel) {
		t.Errorf("user root: %q %v", p, ok)
	}
	projRoot := filepath.Join(root, ".claude", "skills")
	writeAt(t, filepath.Join(projRoot, rel), "project")
	writeAt(t, filepath.Join(projRoot, ".rota-manifest.json"), "{}")
	if p, _ := briefPath(root, roundcfg.Settings{}, env); p != filepath.Join(projRoot, rel) {
		t.Errorf("project root before user: %q", p)
	}
	writeAt(t, filepath.Join(root, "skills", "references", "worker-contract.md"), "checkout")
	if p, _ := briefPath(root, roundcfg.Settings{}, env); p != filepath.Join(root, "skills", "references", "worker-contract.md") {
		t.Errorf("source checkout first: %q", p)
	}
	// CLAUDE_CONFIG_DIR replaces ~/.claude as the user root.
	cfgDir = t.TempDir()
	if p, _ := briefPath(root, roundcfg.Settings{}, env); p != filepath.Join(root, "skills", "references", "worker-contract.md") {
		t.Errorf("checkout still first: %q", p)
	}
	os.Remove(filepath.Join(root, "skills", "references", "worker-contract.md"))
	os.RemoveAll(projRoot)
	if p, ok := briefPath(root, roundcfg.Settings{}, env); ok {
		t.Errorf("~/.claude used although CLAUDE_CONFIG_DIR is set: %q", p)
	}
	writeAt(t, filepath.Join(cfgDir, "skills", ".rota-manifest.json"), "{}")
	writeAt(t, filepath.Join(cfgDir, "skills", rel), "cfg")
	if p, _ := briefPath(root, roundcfg.Settings{}, env); p != filepath.Join(cfgDir, "skills", rel) {
		t.Errorf("config dir root: %q", p)
	}
	cfgDir = ""
	writeAt(t, filepath.Join(root, "mine.md"), "own")
	if p, _ := briefPath(root, roundcfg.Settings{Brief: "mine.md"}, env); p != filepath.Join(root, "mine.md") {
		t.Errorf("round.brief first: %q", p)
	}
	if _, ok := briefPath(root, roundcfg.Settings{Brief: "gone.md"}, env); ok {
		t.Error("round.brief set but missing must not fall back")
	}
}

// A Codex-only install (.agents/skills) carries the contract too.
func TestBriefPathCodexRoots(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	env := func(k string) string {
		if k == "HOME" {
			return home
		}
		return ""
	}
	rel := filepath.Join("rota-orchestrate", "references", "worker-contract.md")
	userRoot := filepath.Join(home, ".agents", "skills")
	writeAt(t, filepath.Join(userRoot, ".rota-manifest.json"), "{}")
	writeAt(t, filepath.Join(userRoot, rel), "user")
	if p, ok := briefPath(root, roundcfg.Settings{}, env); !ok || p != filepath.Join(userRoot, rel) {
		t.Errorf("user codex root: %q %v", p, ok)
	}
	projRoot := filepath.Join(root, ".agents", "skills")
	writeAt(t, filepath.Join(projRoot, ".rota-manifest.json"), "{}")
	writeAt(t, filepath.Join(projRoot, rel), "project")
	if p, _ := briefPath(root, roundcfg.Settings{}, env); p != filepath.Join(projRoot, rel) {
		t.Errorf("project codex root before user: %q", p)
	}
}
