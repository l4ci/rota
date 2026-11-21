package hostenv

import (
	"os"
	"strings"
	"testing"
)

const prefixFile = "../../test/lib/host-env-prefixes"

func TestIsCoversEveryPrefix(t *testing.T) {
	for _, name := range []string{"HERDR_PANE_ID", "HC_SESSION", "ORCA_PANE", "TMUX", "TMUX_PANE"} {
		if !is(name) || !IsKV(name+"=v") {
			t.Errorf("%s not recognised as a host variable", name)
		}
	}
	for _, name := range []string{"HOME", "PATH", "XHERDR_X", "HCX", "SSH_AUTH_SOCK"} {
		if is(name) {
			t.Errorf("%s wrongly recognised as a host variable", name)
		}
	}
}

// The shell harness reads the prefixes from this file; it must match the Go
// list. Regenerate with: go run ./internal/hostenv/gen > test/lib/host-env-prefixes
func TestShellPrefixFileMatchesList(t *testing.T) {
	want := strings.Join(Prefixes, "\n") + "\n"
	got, err := os.ReadFile(prefixFile)
	if err != nil || string(got) != want {
		t.Fatalf("%s is stale or missing (%v); from the repo root run: go run ./internal/hostenv/gen > test/lib/host-env-prefixes", prefixFile, err)
	}
}

// Harness config dirs point at the developer's real claude/codex config; a test
// process must not inherit them (#3838). HOME stays out: testiso and the
// sandboxes pin it themselves where it matters.
func TestScrubDropsHarnessConfigDirs(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/real/claude")
	t.Setenv("CODEX_HOME", "/real/codex")
	t.Setenv("HOME", "/real/home")
	Scrub()
	for _, k := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME"} {
		if os.Getenv(k) != "" {
			t.Errorf("%s survived Scrub", k)
		}
	}
	if os.Getenv("HOME") != "/real/home" {
		t.Error("Scrub must leave HOME alone")
	}
}
