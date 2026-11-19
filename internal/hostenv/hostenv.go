// Package hostenv owns the one rule for which environment variables name a
// host session (herdr, herdr-core, Orca, tmux). Go test isolation (testiso),
// worker sandboxes (worker.IsolatedEnviron) and the shell harness
// (test/lib/isolate.sh, via test/lib/host-env-prefixes) all derive from it, so
// a new host kind is added here and nowhere else.
package hostenv

import (
	"os"
	"strings"
)

// Prefixes are matched against the variable name. TMUX has no underscore on
// purpose: it covers both TMUX and TMUX_PANE. CLAUDE_CONFIG_DIR and CODEX_HOME
// are not host sessions but point a process at the developer's real harness
// config, so they share the list and its three consumers (#3838). HOME stays
// off it: testiso and the sandboxes pin HOME where it matters, and scrubbing
// it would break every child that needs one.
var Prefixes = []string{"HERDR_", "HC_", "ORCA_", "TMUX", "CLAUDE_CONFIG_DIR", "CODEX_HOME"}

// Is reports whether the variable name belongs to a host session or names a
// harness config dir.
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

// Scrub unsets host-session variables from the process environment. The shell
// harness applies the same prefix rule through the generated prefix file.
func Scrub() {
	for _, kv := range os.Environ() {
		if IsKV(kv) {
			key, _, _ := strings.Cut(kv, "=")
			os.Unsetenv(key)
		}
	}
}
