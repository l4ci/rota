package round

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/skills"
	"github.com/l4ci/rota/internal/worker"
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

// briefContractEnv keeps both installed roots and cache extraction in temp dirs.
func briefContractEnv(t *testing.T) (Env, string, string, string) {
	t.Helper()
	root, home, userCache := t.TempDir(), t.TempDir(), t.TempDir()
	getenv := func(k string) string {
		switch k {
		case "HOME":
			return home
		case "ROTA_CACHE_DIR":
			return userCache
		case "CLAUDE_PLUGIN_ROOT":
			return filepath.Join(home, "plugin")
		}
		return ""
	}
	return Env{Worker: wk(worker.Env{Getenv: getenv, Config: func(string) any { return nil }})}, root, home, filepath.Join(userCache, "rota", "skills")
}

// round.brief wins, then the project's checkout copy, then an installed
// skills root (its layout and order are pinned in the skills package).
// CLAUDE_PLUGIN_ROOT is no longer read.
func TestBriefContractOrder(t *testing.T) {
	e, root, home, cache := briefContractEnv(t)
	writeAt(t, filepath.Join(home, "plugin", "references", "worker-contract.md"), "plugin")
	if p, warning, err := e.briefContract(root, roundcfg.Settings{}); err != nil || !strings.HasPrefix(p, cache) || warning != "" {
		t.Fatalf("plugin root must be ignored in favour of cache: %q %q %v", p, warning, err)
	}
	checkout := filepath.Join(root, "skills", "references", "worker-contract.md")
	writeAt(t, checkout, "checkout")
	if p, warning, err := e.briefContract(root, roundcfg.Settings{}); p != checkout || warning != "" || err != nil {
		t.Errorf("source checkout: %q %q %v", p, warning, err)
	}
	own := filepath.Join(root, "mine.md")
	writeAt(t, own, "own")
	if p, warning, err := e.briefContract(root, roundcfg.Settings{Brief: "mine.md"}); p != own || warning != "" || err != nil {
		t.Errorf("round.brief first: %q %q %v", p, warning, err)
	}
}

// A stub install has a manifest but no references: the contract then comes
// from the skills cache, not the stub root.
func TestBriefContractStubRootUsesCache(t *testing.T) {
	e, root, home, cache := briefContractEnv(t)
	set, err := skills.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	stubRoot := filepath.Join(home, ".claude", "skills")
	if _, err := set.Install([]skills.Root{{Path: stubRoot}}, skills.Options{Version: "t", Mode: skills.ModeStub}); err != nil {
		t.Fatal(err)
	}
	p, warning, err := e.briefContract(root, roundcfg.Settings{})
	if err != nil || !strings.HasPrefix(p, cache) || warning != "" {
		t.Fatalf("stub root must fall back to the cache: %q %q %v", p, warning, err)
	}
}

// Without a checkout or install, extract a readable contract into the cache
// and reuse it on later lookups.
func TestBriefContractFallsBackToTheSkillsCache(t *testing.T) {
	e, root, _, cache := briefContractEnv(t)
	p, warning, err := e.briefContract(root, roundcfg.Settings{})
	if err != nil || !strings.HasPrefix(p, cache) || filepath.Base(p) != "worker-contract.md" || warning != "" {
		t.Fatalf("no cache fallback: %q %q %v", p, warning, err)
	}
	if b, err := os.ReadFile(p); err != nil || !strings.Contains(string(b), "Worker contract") {
		t.Errorf("cached contract unreadable: %v", err)
	}
	if p2, warning, err := e.briefContract(root, roundcfg.Settings{}); p2 != p || warning != "" || err != nil {
		t.Errorf("second lookup moved: %q vs %q, warning %q, error %v", p2, p, warning, err)
	}
}

// Missing configured briefs refuse outside the cache; stale cache paths
// recover with a warning, while existing cache paths win silently (#2229).
func TestBriefContractMissingAndStale(t *testing.T) {
	e, root, _, cache := briefContractEnv(t)
	writeAt(t, filepath.Join(root, "skills", "references", "worker-contract.md"), "checkout")
	outside := filepath.Join(root, "gone.md")
	p, warning, err := e.briefContract(root, roundcfg.Settings{Brief: outside})
	if blockedBy(t, err) != blockedby.BriefMissing || p != "" || warning != "" {
		t.Fatalf("missing brief must refuse: %q %q %v", p, warning, err)
	}
	if !strings.Contains(err.Error(), outside) || !strings.Contains(err.Error(), cache) || !strings.Contains(err.Error(), "set round.brief to an existing file or unset it") {
		t.Errorf("refusal must name the configured value, cache and recovery: %v", err)
	}

	stale := filepath.Join(cache, "0.26.0", "references", "worker-contract.md")
	p, warning, err = e.briefContract(root, roundcfg.Settings{Brief: stale})
	if err != nil || !strings.HasPrefix(p, cache) || p == stale || filepath.Base(p) != "worker-contract.md" {
		t.Fatalf("no stale-cache fallback: %q %v", p, err)
	}
	if !strings.Contains(warning, stale) || !strings.Contains(warning, p) {
		t.Errorf("warning must name the stale value and the path used: %q", warning)
	}

	good := filepath.Join(cache, "0.27.0", "references", "worker-contract.md")
	writeAt(t, good, "mine")
	p, warning, err = e.briefContract(root, roundcfg.Settings{Brief: good})
	if err != nil || p != good || warning != "" {
		t.Errorf("an existing round.brief must win silently: %q %q %v", p, warning, err)
	}
}
