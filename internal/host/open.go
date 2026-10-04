package host

import (
	"context"
	"fmt"
	"strings"
	"time"
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
	var run Result
	for i := 0; i < paneRetries; i++ {
		run = h.herdr(ctx, "pane", "run", pane, o.Command)
		if run.ExitCode == 0 {
			return tab, nil
		}
		if jget(run.Stderr, "error.code") != "agent_pane_not_found" && jget(run.Stderr, "error.code") != "pane_not_found" {
			break
		}
		h.d.Sleep(200 * time.Millisecond)
	}
	h.herdr(ctx, "tab", "close", tab)
	return "", fmt.Errorf("herdr pane run failed (tab %s closed): %s", tab, strings.TrimSpace(run.Stderr))
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
