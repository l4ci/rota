package host

import (
	"fmt"
	"os"
	"os/exec"
)

// Solo is the round host of a round with no terminal: workers are in-harness
// subagents, so there is no pane to drive. It is a round-level value only:
// New never returns it, and no Host is built for it.
const Solo = "solo"

// The host kinds that have panes.
const (
	Herdr = "herdr"
	Tmux  = "tmux"
)

// Resolve is the rule for which host a round runs on (C8), the in-session one:
// it never opens a session (see ResolveLaunch for the launcher that may). The round's
// recorded host (registry, "" when no round is in flight) wins. Without one it
// picks from work.dispatch and the environment. An explicit herdr or tmux is taken as it is, even when it is
// not installed: that fails later as unavailable (exit 5), and solo is never a
// silent fallback from a host the config named. Unset or "subagent" detects:
// herdr inside a herdr pane with the binary on PATH, else tmux inside tmux,
// else solo. getenv and lookPath default to the process's own.
func Resolve(registry, dispatch string, getenv func(string) string, lookPath func(string) (string, error)) string {
	if getenv == nil {
		getenv = os.Getenv
	}
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if registry != "" {
		return registry
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

// Launch is where a launcher that may open its own session puts itself.
type Launch struct {
	Kind string // Herdr, Tmux or Solo
	// NewSession: the caller is in no pane of Kind, so the launcher opens a
	// session of its own (named, never the user's default) instead of a tab.
	NewSession bool
}

// ResolveLaunch is Resolve's variant for a launcher that may open its own
// session (`rota orchestrate`): the same kinds, a different question. Resolve
// asks which host a round's workers run on, so it answers solo when the caller
// is in no pane. ResolveLaunch asks where to start, so with no pane it opens a
// session rather than falling back to solo. Four differences follow, all
// deliberate:
//
//   - Being in a pane (herdr: HERDR_ENV=1 and HERDR_WORKSPACE_ID, with the binary
//     on PATH; tmux: TMUX) beats work.dispatch, which Resolve checks first: you
//     cannot start a tab in a host you are not in.
//   - A herdr pane needs HERDR_WORKSPACE_ID too (herdr.InSession); Resolve takes
//     HERDR_ENV=1 with the binary on PATH as a herdr pane.
//   - Outside any pane, an explicit dispatch picks the kind and must be installed
//     (an error otherwise); unset or "subagent" prefers herdr, then tmux, then
//     solo with no session.
//   - There is no registry: the launcher starts before any round exists.
//
// getenv and lookPath default to the process's own.
func ResolveLaunch(dispatch string, getenv func(string) string, lookPath func(string) (string, error)) (Launch, error) {
	d := Deps{Getenv: getenv, LookPath: lookPath}
	herdr, tmux := New(Herdr, d), New(Tmux, d)
	switch {
	case herdr.InSession() && herdr.Require() == nil:
		return Launch{Kind: Herdr}, nil
	case tmux.InSession():
		return Launch{Kind: Tmux}, nil
	case dispatch == Herdr && herdr.Require() != nil:
		return Launch{}, fmt.Errorf("work.dispatch=herdr but herdr is not installed")
	case dispatch == Tmux && tmux.Require() != nil:
		return Launch{}, fmt.Errorf("work.dispatch=tmux but tmux is not installed")
	case dispatch != Tmux && herdr.Require() == nil:
		return Launch{Kind: Herdr, NewSession: true}, nil
	case tmux.Require() == nil:
		return Launch{Kind: Tmux, NewSession: true}, nil
	}
	return Launch{Kind: Solo}, nil
}
