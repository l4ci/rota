package host

import (
	"os"
	"testing"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// bootReady is the one text-readiness rule: tmux and herdr call it with
// requireInput=false (any boot evidence), Orca with true (loaded model plus a
// composer prompt where the harness declares a model row).
func TestBootReady(t *testing.T) {
	for _, c := range []struct {
		name          string
		text          string
		loose, strict bool
	}{
		{"claude booted", fixture(t, "claude-booted-2.1.289.txt"), true, true},
		{"codex booted fixture (no model row)", fixture(t, "codex-booted-0.159.2.txt"), true, false},
		{"opencode booted", fixture(t, "opencode-booted-1.17.9.txt"), true, false},
		{"codex loaded", "model: gpt-5.3-codex\n› Ask Codex to do anything", true, true},
		{"codex loading", "model: loading\n› Ask Codex to do anything", true, false},
		{"banner only", "Welcome to Claude Code", true, false},
		{"trust dialog", fixture(t, "trust-dialog-2.1.288.txt"), false, false},
		{"shell", "$ claude --version\n", false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := bootReady(c.text, false); got != c.loose {
				t.Errorf("loose = %v, want %v", got, c.loose)
			}
			if got := bootReady(c.text, true); got != c.strict {
				t.Errorf("strict = %v, want %v", got, c.strict)
			}
		})
	}
}

// The poll path must reuse the cached patterns, not compile per call.
func TestBootReadyCompilesOnce(t *testing.T) {
	text := fixture(t, "claude-booted-2.1.289.txt")
	first := loadBootRules()
	for i := 0; i < 5; i++ {
		bootReady(text, false)
		bootReady(text, true)
	}
	if loadBootRules() != first {
		t.Fatal("rules were rebuilt between calls")
	}
	if bootRuleLoads != 1 {
		t.Fatalf("patterns compiled %d times, want 1", bootRuleLoads)
	}
}

// Strict mode must not widen to a harness BootPattern alone: the Codex
// placeholder without a model row and prompt is not ready for Orca.
func TestBootReadyStrictIgnoresHarnessBootPatterns(t *testing.T) {
	if bootReady("Ask Codex to do anything", true) {
		t.Fatal("placeholder alone must not be strict-ready")
	}
}
