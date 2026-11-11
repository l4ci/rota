package hostenv

import (
	"os"
	"strings"
	"testing"
)

const prefixFile = "../../test/lib/host-env-prefixes"

func TestIsCoversEveryPrefix(t *testing.T) {
	for _, name := range []string{"HERDR_PANE_ID", "HC_SESSION", "ORCA_PANE", "TMUX", "TMUX_PANE"} {
		if !Is(name) || !IsKV(name+"=v") {
			t.Errorf("%s not recognised as a host variable", name)
		}
	}
	for _, name := range []string{"HOME", "PATH", "XHERDR_X", "HCX", "SSH_AUTH_SOCK"} {
		if Is(name) {
			t.Errorf("%s wrongly recognised as a host variable", name)
		}
	}
}

// The shell harness reads the prefixes from this file; it must match the Go
// list. Regenerate with: go test ./internal/hostenv -update-prefixes
func TestShellPrefixFileMatchesList(t *testing.T) {
	want := strings.Join(Prefixes, "\n") + "\n"
	got, err := os.ReadFile(prefixFile)
	if err != nil || string(got) != want {
		t.Fatalf("%s is stale or missing (%v); run: go run ./internal/hostenv/gen > %s", prefixFile, err, prefixFile)
	}
}
