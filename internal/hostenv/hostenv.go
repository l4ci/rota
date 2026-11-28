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
// purpose: it covers both TMUX and TMUX_PANE.
var Prefixes = []string{"HERDR_", "HC_", "ORCA_", "TMUX"}

// ConfigVars are exact names that point a process at the developer's real
// harness config (#3838). They are not host sessions, so they stay out of
// Prefixes and the shell prefix file: test/runner.sh pins CLAUDE_CONFIG_DIR to
// a private dir itself, and isolate_env unsets CODEX_HOME. HOME is not listed:
// testiso leaves it, and worker and shell sandboxes pin it.
var ConfigVars = []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME"}

// is reports whether the variable name belongs to a host session.
func is(name string) bool {
	for _, p := range Prefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// IsolationVars are the exact names that both sandboxes drop:
// worker.IsolatedEnviron and isolate_env (test/lib/isolate.sh, via the
// generated test/lib/isolation-env-names). They reach the developer's
// ssh-agent, global rota config, Core state or codex config, and the override
// variables win over the pinned HOME and XDG dirs.
var IsolationVars = []string{"SSH_AUTH_SOCK", "SSH_AGENT_PID", "ROTA_CONFIG_DIR", "ROTA_CORE_STATE_DIR", "CODEX_HOME"}

// Drop reports whether a test or sandbox process must not inherit the variable:
// a host session or a harness config dir.
func Drop(name string) bool {
	for _, v := range ConfigVars {
		if name == v {
			return true
		}
	}
	return is(name)
}

// isKV is is for a KEY=value entry as os.Environ returns it.
func isKV(kv string) bool {
	k, _, _ := strings.Cut(kv, "=")
	return is(k)
}

// Scrub unsets host-session and harness-config variables from the process environment. The shell
// harness applies the same prefix rule through the generated prefix file.
func Scrub() {
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if Drop(key) {
			os.Unsetenv(key)
		}
	}
}
