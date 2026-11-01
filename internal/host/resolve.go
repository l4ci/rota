package host

import "fmt"

// herdrPaneFacts holds facts, not a host-selection policy. Resolve needs the marker
// and binary; InSession needs the marker and workspace even without the binary.
type herdrPaneFacts struct {
	managed, workspace, installed bool
}

// detectHerdrPane is the shared source of herdr pane facts. Callers supply
// filled dependencies and retain their deliberately different policies.
func detectHerdrPane(d Deps) herdrPaneFacts {
	_, err := d.LookPath(Herdr.Binary())
	return herdrPaneFacts{
		managed:   d.Getenv("HERDR_ENV") == "1",
		workspace: d.Getenv("HERDR_WORKSPACE_ID") != "",
		installed: err == nil,
	}
}

// Resolve is the rule for which host a round runs on (C8), the in-session one:
// it never opens a session (see ResolveLaunch for the launcher that may). The round's
// recorded host (registry, "" when no round is in flight) wins. Without one it
// picks from round.dispatch and the environment. An explicit herdr, tmux or orca is taken as it is, even when it is
// not installed: that fails later as unavailable (exit 5), and solo is never a
// silent fallback from a host the config named. Unset or "subagent" detects:
// herdr inside a herdr pane with the binary on PATH, else tmux inside tmux,
// else solo. getenv and lookPath default to the process's own.
func Resolve(registry, dispatch Kind, getenv func(string) string, lookPath func(string) (string, error)) Kind {
	if registry != "" {
		return registry
	}
	switch dispatch {
	case Herdr, Tmux, Orca:
		return dispatch
	}
	d := Deps{Getenv: getenv, LookPath: lookPath}
	d.fill()
	pane := detectHerdrPane(d)
	if pane.managed && pane.installed {
		return Herdr
	}
	if d.Getenv("TMUX") != "" {
		return Tmux
	}
	return Solo
}

// Launch is where a launcher that may open its own session puts itself.
type Launch struct {
	Kind Kind // Herdr, Tmux, Orca or Solo
	// NewSession: the caller is in no pane of Kind, so the launcher opens a
	// session of its own (named, never the user's default) instead of a tab.
	NewSession bool
}

// ResolveLaunch is Resolve's variant for a launcher that may open its own
// session (`rota orchestrate`): the same kinds, a different question. Resolve
// asks which host a round's workers run on, so it answers solo when the caller
// is in no pane. ResolveLaunch asks where to start, so with no pane it opens a
// session rather than falling back to solo. The differences are deliberate:
//
//   - Being in a pane (herdr: HERDR_ENV=1 and HERDR_WORKSPACE_ID, with the binary
//     on PATH; tmux: TMUX) beats round.dispatch, which Resolve checks first: you
//     cannot start a tab in a host you are not in.
//   - A herdr pane needs HERDR_WORKSPACE_ID too (herdr.InSession); Resolve takes
//     HERDR_ENV=1 with the binary on PATH as a herdr pane.
//   - Outside any pane, an explicit dispatch picks the kind and must be installed
//     (an error otherwise); unset or "subagent" prefers herdr, then tmux, then
//     solo with no session.
//   - Explicit orca outside a herdr/tmux pane opens a terminal in its running
//     app, without a named session. Orca is never automatically detected.
//   - There is no registry: the launcher starts before any round exists.
//
// getenv and lookPath default to the process's own.
func ResolveLaunch(dispatch Kind, getenv func(string) string, lookPath func(string) (string, error)) (Launch, error) {
	d := Deps{Getenv: getenv, LookPath: lookPath}
	herdr, tmux := New(Herdr, d), New(Tmux, d)
	switch {
	case herdr.InSession() && herdr.Require() == nil:
		return Launch{Kind: Herdr}, nil
	case tmux.InSession():
		return Launch{Kind: Tmux}, nil
	case dispatch == Orca:
		d.fill()
		if _, err := d.LookPath(Orca.Binary()); err != nil {
			return Launch{}, fmt.Errorf("round.dispatch=orca but orca is not installed")
		}
		return Launch{Kind: Orca}, nil
	case dispatch == Herdr && herdr.Require() != nil:
		return Launch{}, fmt.Errorf("round.dispatch=herdr but herdr is not installed")
	case dispatch == Tmux && tmux.Require() != nil:
		return Launch{}, fmt.Errorf("round.dispatch=tmux but tmux is not installed")
	case dispatch != Tmux && herdr.Require() == nil:
		return Launch{Kind: Herdr, NewSession: true}, nil
	case tmux.Require() == nil:
		return Launch{Kind: Tmux, NewSession: true}, nil
	}
	return Launch{Kind: Solo}, nil
}
