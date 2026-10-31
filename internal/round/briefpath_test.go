package round

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/skills"
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

// round.brief wins, then the project's checkout copy, then an installed
// skills root (its layout and order are pinned in the skills package).
// CLAUDE_PLUGIN_ROOT is no longer read.
func TestBriefPathOrder(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	env := func(k string) string {
		switch k {
		case "HOME":
			return home
		case "CLAUDE_PLUGIN_ROOT":
			return filepath.Join(home, "plugin")
		}
		return ""
	}
	writeAt(t, filepath.Join(home, "plugin", "references", "worker-contract.md"), "plugin")
	if p, ok := briefPath(root, roundcfg.Settings{}, env, ""); ok {
		t.Fatalf("CLAUDE_PLUGIN_ROOT still read: %s", p)
	}
	checkout := skills.CheckoutBrief(root)
	if checkout != "" {
		t.Fatalf("empty root has a checkout brief: %q", checkout)
	}
	writeAt(t, filepath.Join(root, "skills", "references", "worker-contract.md"), "checkout")
	if p, _ := briefPath(root, roundcfg.Settings{}, env, ""); p != skills.CheckoutBrief(root) {
		t.Errorf("source checkout: %q", p)
	}
	writeAt(t, filepath.Join(root, "mine.md"), "own")
	if p, _ := briefPath(root, roundcfg.Settings{Brief: "mine.md"}, env, ""); p != filepath.Join(root, "mine.md") {
		t.Errorf("round.brief first: %q", p)
	}
	if _, ok := briefPath(root, roundcfg.Settings{Brief: "gone.md"}, env, ""); ok {
		t.Error("round.brief set but missing must not fall back")
	}
}

// A stub install has a manifest but no references: the contract then comes
// from the skills cache, not the stub root.
func TestBriefPathStubRootUsesCache(t *testing.T) {
	root, home, cache := t.TempDir(), t.TempDir(), t.TempDir()
	env := func(k string) string {
		if k == "HOME" {
			return home
		}
		return ""
	}
	set, err := skills.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	stubRoot := filepath.Join(home, ".claude", "skills")
	if _, err := set.Install([]skills.Root{{Path: stubRoot}}, skills.Options{Version: "t", Mode: skills.ModeStub}); err != nil {
		t.Fatal(err)
	}
	p, ok := briefPath(root, roundcfg.Settings{}, env, cache)
	if !ok || !strings.HasPrefix(p, cache) {
		t.Fatalf("stub root must fall back to the cache: %q %v", p, ok)
	}
}

// With round.brief unset and neither a checkout copy nor an installed skills
// root, the contract comes from the skills cache, extracted from the binary
// when the cache has none yet. A set round.brief that is missing stays a miss.
func TestBriefPathFallsBackToTheSkillsCache(t *testing.T) {
	root, home, cache := t.TempDir(), t.TempDir(), t.TempDir()
	env := func(k string) string {
		if k == "HOME" {
			return home
		}
		return ""
	}
	p, ok := briefPath(root, roundcfg.Settings{}, env, cache)
	if !ok || !strings.HasPrefix(p, cache) || filepath.Base(p) != "worker-contract.md" {
		t.Fatalf("no cache fallback: %q %v", p, ok)
	}
	if b, err := os.ReadFile(p); err != nil || !strings.Contains(string(b), "Worker contract") {
		t.Errorf("cached contract unreadable: %v", err)
	}
	if p2, _ := briefPath(root, roundcfg.Settings{}, env, cache); p2 != p {
		t.Errorf("second lookup moved: %q vs %q", p2, p)
	}
	if _, ok := briefPath(root, roundcfg.Settings{Brief: "gone.md"}, env, cache); ok {
		t.Error("a missing round.brief fell back to the cache")
	}
}

// A round.brief that points into rota's own skills cache at a tree an update
// removed falls back to the current cache copy with a warning naming both; one
// outside the cache root stays a miss; one that exists is used first (#2229).
func TestBriefPathStaleCacheBrief(t *testing.T) {
	root, home, cache := t.TempDir(), t.TempDir(), t.TempDir()
	env := func(k string) string {
		if k == "HOME" {
			return home
		}
		return ""
	}
	stale := filepath.Join(cache, "0.26.0", "references", "worker-contract.md")
	p, warn, ok := resolveBrief(root, roundcfg.Settings{Brief: stale}, env, cache)
	if !ok || !strings.HasPrefix(p, cache) || p == stale || filepath.Base(p) != "worker-contract.md" {
		t.Fatalf("no stale-cache fallback: %q %v", p, ok)
	}
	if !strings.Contains(warn, stale) || !strings.Contains(warn, p) {
		t.Errorf("warning must name the stale value and the path used: %q", warn)
	}

	outside := filepath.Join(t.TempDir(), "gone.md")
	p, warn, ok = resolveBrief(root, roundcfg.Settings{Brief: outside}, env, cache)
	if ok || warn != "" {
		t.Errorf("outside the cache root must stay a miss: %q %q %v", p, warn, ok)
	}
	if msg := briefMissingMsg(roundcfg.Settings{Brief: outside}, cache); !strings.Contains(msg, outside) || !strings.Contains(msg, cache) {
		t.Errorf("refusal must name the configured value and the cache path: %q", msg)
	}

	good := filepath.Join(cache, "0.27.0", "references", "worker-contract.md")
	writeAt(t, good, "mine")
	p, warn, ok = resolveBrief(root, roundcfg.Settings{Brief: good}, env, cache)
	if !ok || p != good || warn != "" {
		t.Errorf("an existing round.brief must win silently: %q %q %v", p, warn, ok)
	}
}
