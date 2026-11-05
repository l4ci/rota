package harness

import "testing"

func TestStartupDialogRecoveryGenericFallback(t *testing.T) {
	for _, kind := range []string{"", "unknown-harness", "opencode"} {
		t.Run(kind, func(t *testing.T) {
			// An empty hint leaves the caller's generic worker-terminal
			// guidance intact rather than borrowing another adapter's hint.
			if got := StartupDialogRecovery(kind); got != "" {
				t.Fatalf("StartupDialogRecovery(%q) = %q, want generic fallback", kind, got)
			}
		})
	}
}
