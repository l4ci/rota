package host

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/shlex"
)

// TabOpts describes the tab an interactive session runs in: the orchestrator's,
// not a worker slot's. A person is meant to watch it.
type TabOpts struct {
	Label string
	Cwd   string
	// Command is a shell line typed into the new tab's shell. The tab stays at
	// its prompt when the command exits, so a failure message is not lost.
	Command string
}

// TabOpener hosts can open a focused tab running a command for a person.
type TabOpener interface {
	// OpenTab returns the tab's handle (a herdr tab id, a tmux window target).
	OpenTab(ctx context.Context, o TabOpts) (string, error)
}

// paneRetries is how often `pane run` is retried on pane_not_found: a pane
// created a moment ago can be unknown to the server for a beat (maestro saw it
// on herdr 0.8.2 and retries five times).
const paneRetries = 5

// OpenTab creates a focused tab in the caller's workspace and runs the command
// in its root pane. Unlike Spawn it does not use `agent start`: the command is
// a supervisor (`rota keepalive run`), not the agent binary, and a person is
// there to answer a first-run trust dialog.
func (h *herdr) OpenTab(ctx context.Context, o TabOpts) (string, error) {
	r := h.herdr(ctx, "tab", "create", "--workspace", h.d.Getenv("HERDR_WORKSPACE_ID"),
		"--cwd", o.Cwd, "--label", o.Label, "--focus")
	if r.ExitCode != 0 {
		return "", fmt.Errorf("herdr tab create failed: %s", strings.TrimSpace(r.Stderr))
	}
	tab := jget(r.Stdout, "result.tab.tab_id")
	pane := jget(r.Stdout, "result.root_pane.pane_id")
	if tab == "" || pane == "" {
		return "", fmt.Errorf("herdr tab create returned no tab/pane id")
	}
	if err := h.paneRun(ctx, "", pane, o.Command); err != nil {
		h.herdr(ctx, "tab", "close", tab)
		return "", fmt.Errorf("%v (tab %s closed)", err, tab)
	}
	return tab, nil
}

// paneRun types the command into the pane (of the named session, "" for the
// caller's own), retrying a pane the server does not know yet.
func (h *herdr) paneRun(ctx context.Context, session, pane, command string) error {
	args := []string{"pane", "run", pane, command}
	if session != "" {
		args = append([]string{"--session", session}, args...)
	}
	var run Result
	for i := 0; i < paneRetries; i++ {
		run = h.herdr(ctx, args...)
		if run.ExitCode == 0 {
			return nil
		}
		if jget(run.Stderr, "error.code") != "agent_pane_not_found" && jget(run.Stderr, "error.code") != "pane_not_found" {
			break
		}
		h.d.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("herdr pane run failed: %s", strings.TrimSpace(run.Stderr))
}

// SessionStarter hosts can start a named persistent session of their own,
// from outside the backend, with the command already running in it.
type SessionStarter interface {
	// StartSession leaves the named session running with a workspace whose
	// pane runs the command, and reports whether it created the session. A
	// session that already runs is left alone: attaching shows it, and the
	// keepalive lease refuses a second orchestrator.
	StartSession(ctx context.Context, name string, o TabOpts) (created bool, err error)
}

// serverRetries and serverWait bound the wait for a new headless server's socket.
const (
	serverRetries = 25
	serverWait    = 200 * time.Millisecond
)

// StartSession starts `herdr --session <name> server` headless, then makes a
// workspace and runs the command in its pane over that session's socket. herdr
// has no startup-command flag, and its guide forbids driving the user's own
// session from outside; a session this call created is rota's. Verified on
// herdr 0.9.3 (docs/usage/orchestrator-harnesses.md).
func (h *herdr) StartSession(ctx context.Context, name string, o TabOpts) (bool, error) {
	if h.herdr(ctx, "--session", name, "status", "server").ExitCode == 0 {
		return false, nil
	}
	// The shell backgrounds the server and returns, so Run does not block on it.
	start := shlex.Join([]string{"nohup", "herdr", "--session", name, "server"}) + " >/dev/null 2>&1 &"
	if r, err := h.d.Run(ctx, "sh", []string{"-c", start}); err != nil || r.ExitCode != 0 {
		return false, fmt.Errorf("cannot start herdr server for session %s", name)
	}
	up := false
	for i := 0; i < serverRetries && !up; i++ {
		if up = h.herdr(ctx, "--session", name, "status", "server").ExitCode == 0; !up {
			h.d.Sleep(serverWait)
		}
	}
	if !up {
		return false, fmt.Errorf("herdr session %s did not come up", name)
	}
	r := h.herdr(ctx, "--session", name, "workspace", "create", "--cwd", o.Cwd, "--label", o.Label, "--focus")
	if r.ExitCode != 0 {
		return false, fmt.Errorf("herdr workspace create failed: %s", strings.TrimSpace(r.Stderr))
	}
	pane := jget(r.Stdout, "result.root_pane.pane_id")
	if pane == "" {
		return false, fmt.Errorf("herdr workspace create returned no pane id")
	}
	if err := h.paneRun(ctx, name, pane, o.Command); err != nil {
		return false, err
	}
	return true, nil
}

// OpenTab creates a window in the caller's tmux session, selects it and types
// the command into its shell.
func (t *tmux) OpenTab(ctx context.Context, o TabOpts) (string, error) {
	r := t.tmux(ctx, "new-window", "-P", "-F", "#{window_id}", "-n", o.Label, "-c", o.Cwd)
	if r.ExitCode != 0 {
		return "", fmt.Errorf("tmux new-window failed: %s", strings.TrimSpace(r.Stderr))
	}
	win := strings.TrimSpace(r.Stdout)
	if win == "" {
		return "", fmt.Errorf("tmux new-window returned no window id")
	}
	if s := t.tmux(ctx, "send-keys", "-t", win, o.Command, "C-m"); s.ExitCode != 0 {
		t.tmux(ctx, "kill-window", "-t", win)
		return "", fmt.Errorf("tmux send-keys failed (window %s closed): %s", win, strings.TrimSpace(s.Stderr))
	}
	return win, nil
}
