package host

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// herdrPane is one entry of `herdr pane list`.
type herdrPane struct {
	PaneID      string `json:"pane_id"`
	TabID       string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
	Cwd         string `json:"cwd"`
	FgCwd       string `json:"foreground_cwd"`
	Agent       string `json:"agent"`
	Status      string `json:"agent_status"`
}

// listPanes decodes `herdr pane list`.
func (h *herdr) listPanes(ctx context.Context) ([]herdrPane, error) {
	r := h.herdr(ctx, "pane", "list")
	if r.ExitCode != 0 {
		return nil, fmt.Errorf("herdr pane list failed: %s", strings.TrimSpace(r.Stderr))
	}
	var doc struct {
		Result struct {
			Panes []herdrPane `json:"panes"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &doc); err != nil {
		return nil, fmt.Errorf("herdr pane list: unreadable reply: %w", err)
	}
	return doc.Result.Panes, nil
}

// agentless is true when herdr reports no agent for the pane and its
// foreground processes were read and are all shells.
func (h *herdr) agentless(ctx context.Context, p herdrPane) bool {
	return p.Agent == "" && (p.Status == "unknown" || p.Status == "") && h.onlyShells(ctx, p.PaneID)
}
