package cli

import (
	"testing"

	"github.com/l4ci/rota/internal/config"
)

func TestCoreSetupSuggestsAssociatedRuntimeAndSupportedHarness(t *testing.T) {
	dir := signalRepo(t, "git@github.com:acme/x.git")
	code, out, _ := signalRunWith(t, dir, signalOpts{onPath: []string{"herdr", "claude", "codex"}, env: map[string]string{"HC_TERMINAL_ID": "t1", "HC_TERMINAL_SOCKET": "/associated.sock"}}, "--list", "--json")
	if code != 0 {
		t.Fatalf("setup list: %d %s", code, out)
	}
	if got := str(listQuestion(t, out, config.RoundDispatchKey), "suggestion"); got != "herdr-core" {
		t.Fatalf("Core setup suggests disconnected host %q", got)
	}
	if got := str(listQuestion(t, out, config.RoundHarnessesKey), "suggestion"); got != `["claude"]` {
		t.Fatalf("Core setup suggests unsupported worker: %q", got)
	}
}
