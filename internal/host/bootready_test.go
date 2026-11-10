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

// The poll path must not compile a regexp per call.
func TestBootReadyDoesNotCompile(t *testing.T) {
	text := fixture(t, "claude-booted-2.1.289.txt")
	bootReady(text, true) // warm the cache
	if n := testing.AllocsPerRun(50, func() { bootReady(text, false); bootReady(text, true) }); n > 4 {
		t.Fatalf("%v allocs per call pair; patterns are being compiled on the poll path", n)
	}
}
