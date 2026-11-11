// Package hostenv owns the one rule for which environment variables name a
// host session (herdr, herdr-core, Orca, tmux). Go test isolation (testiso),
// worker sandboxes (worker.IsolatedEnviron) and the shell harness
// (test/lib/isolate.sh, via test/lib/host-env-prefixes) all derive from it, so
// a new host kind is added here and nowhere else.
package hostenv

import "strings"

// Prefixes are matched against the variable name. TMUX has no underscore on
// purpose: it covers both TMUX and TMUX_PANE.
var Prefixes = []string{"HERDR_", "HC_", "ORCA_", "TMUX"}

// Is reports whether the variable name belongs to a host session.
func Is(name string) bool {
	for _, p := range Prefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// IsKV is Is for a KEY=value entry as os.Environ returns it.
func IsKV(kv string) bool {
	k, _, _ := strings.Cut(kv, "=")
	return Is(k)
}
