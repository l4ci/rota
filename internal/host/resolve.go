package host

import (
	"os"
	"os/exec"
)

// Solo is the round host of a round with no terminal: workers are in-harness
// subagents, so there is no pane to drive. It is a round-level value only:
// New never returns it, and no Host is built for it.
const Solo = "solo"

// ResolveRound picks the host a round runs on (C8), from work.dispatch and the
// environment. An explicit herdr or tmux is taken as it is, even when it is
// not installed: that fails later as unavailable (exit 5), and solo is never a
// silent fallback from a host the config named. Unset or "subagent" detects:
// herdr inside a herdr pane with the binary on PATH, else tmux inside tmux,
// else solo. getenv and lookPath default to the process's own.
func ResolveRound(dispatch string, getenv func(string) string, lookPath func(string) (string, error)) string {
	if getenv == nil {
		getenv = os.Getenv
	}
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	switch dispatch {
	case "herdr", "tmux":
		return dispatch
	}
	if getenv("HERDR_ENV") == "1" {
		if _, err := lookPath("herdr"); err == nil {
			return "herdr"
		}
	}
	if getenv("TMUX") != "" {
		return "tmux"
	}
	return Solo
}
