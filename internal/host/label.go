package host

import (
	"context"
	"fmt"
	"strings"
)

// LabelSource is the --source id of every metadata rota reports to herdr, so
// it never overwrites what another tool reports for the same pane.
const LabelSource = "rota"

// herdr 0.9.3 parses the pane or workspace id before the options: with --source
// first it exits 2 ("unknown option: rota"). Keep the id first in every call.

// Label sets the slot pane's title in herdr's sidebar, "" clears it. It uses
// --title, the line herdr otherwise fills with the agent's own terminal title;
// --display-agent only renames the agent-kind label, so it is cleared but never
// set. Best effort: a missing pane is silent, and a failing set writes one
// stderr note per host instance (a clear fails quietly: the pane may be gone).
func (h *herdr) Label(ctx context.Context, slot, handle, title string) {
	pane := h.PaneOf(ctx, slot, handle)
	if pane == "" {
		return
	}
	args := []string{"pane", "report-metadata", pane, "--source", LabelSource}
	if title == "" {
		args = append(args, "--clear-title", "--clear-display-agent")
	} else {
		args = append(args, "--title", title)
	}
	r := h.herdr(ctx, args...)
	if r.ExitCode == 0 || title == "" {
		return
	}
	if h.noted != nil && h.noted.CompareAndSwap(false, true) {
		why, _, _ := strings.Cut(strings.TrimSpace(r.Stderr), "\n")
		fmt.Fprintf(h.d.Stderr, "rota: herdr tab label not set (%s)\n", why)
	}
}

// LabelWorkspace sets the `round` token on the caller's own workspace.
func (h *herdr) LabelWorkspace(ctx context.Context, token string) {
	ws := h.d.Getenv("HERDR_WORKSPACE_ID")
	if ws == "" || token == "" {
		return
	}
	h.herdr(ctx, "workspace", "report-metadata", ws, "--source", LabelSource, "--token", "round="+token)
}
