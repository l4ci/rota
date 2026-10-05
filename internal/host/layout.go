package host

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// LayoutPane is one pane of the herdr server with the labels of the tab and
// workspace it sits in. Agent is the agent kind herdr detected in it, "" for a
// plain shell.
type LayoutPane struct {
	ID, Tab, Workspace      string
	TabLabel, WorkspaceName string
	Cwd, FgCwd, Agent       string
}

// Rect is a pane's cells inside its tab.
type Rect struct{ X, Y, W, H int }

// Layouter is the optional pane-arranging side of a host (herdr 0.9.3 `pane
// move`). It moves live panes only: no process restarts, nothing is typed.
type Layouter interface {
	// PaneOf resolves a slot's handle to the pane of its agent, "" when none.
	PaneOf(ctx context.Context, slot, handle string) string
	// LayoutPanes lists every pane of the server.
	LayoutPanes(ctx context.Context) ([]LayoutPane, error)
	// PaneRects is the layout of the tab holding pane, keyed by pane id.
	PaneRects(ctx context.Context, pane string) (map[string]Rect, error)
	// SplitInto moves pane into tab as a split of target (dir "right" or
	// "down"). keep is the share of target's cell that target keeps.
	SplitInto(ctx context.Context, pane, tab, target, dir string, keep float64) error
	// ToNewTab moves pane out into a new tab at the end, named label.
	ToNewTab(ctx context.Context, pane, label string) error
	// RenameTab labels a tab, RenamePane names a pane.
	RenameTab(ctx context.Context, tab, label string) error
	RenamePane(ctx context.Context, pane, label string) error
}

// LayoutPanes joins `pane list` with the tab and workspace labels.
func (h *herdr) LayoutPanes(ctx context.Context) ([]LayoutPane, error) {
	r := h.herdr(ctx, "pane", "list")
	if r.ExitCode != 0 {
		return nil, fmt.Errorf("herdr pane list failed: %s", strings.TrimSpace(r.Stderr))
	}
	var panes struct {
		Result struct {
			Panes []struct {
				PaneID      string `json:"pane_id"`
				TabID       string `json:"tab_id"`
				WorkspaceID string `json:"workspace_id"`
				Cwd         string `json:"cwd"`
				FgCwd       string `json:"foreground_cwd"`
				Agent       string `json:"agent"`
			} `json:"panes"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &panes); err != nil {
		return nil, fmt.Errorf("herdr pane list: unreadable reply: %w", err)
	}
	tabs, err := h.labels(ctx, "tab", "tabs", "tab_id")
	if err != nil {
		return nil, err
	}
	spaces, err := h.labels(ctx, "workspace", "workspaces", "workspace_id")
	if err != nil {
		return nil, err
	}
	out := make([]LayoutPane, 0, len(panes.Result.Panes))
	for _, p := range panes.Result.Panes {
		out = append(out, LayoutPane{ID: p.PaneID, Tab: p.TabID, Workspace: p.WorkspaceID,
			TabLabel: tabs[p.TabID], WorkspaceName: spaces[p.WorkspaceID],
			Cwd: p.Cwd, FgCwd: p.FgCwd, Agent: p.Agent})
	}
	return out, nil
}

// labels reads `herdr <noun> list`: id to label.
func (h *herdr) labels(ctx context.Context, noun, key, idKey string) (map[string]string, error) {
	r := h.herdr(ctx, noun, "list")
	if r.ExitCode != 0 {
		return nil, fmt.Errorf("herdr %s list failed: %s", noun, strings.TrimSpace(r.Stderr))
	}
	var doc struct {
		Result map[string]json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &doc); err != nil {
		return nil, fmt.Errorf("herdr %s list: unreadable reply: %w", noun, err)
	}
	var list []map[string]any
	if err := json.Unmarshal(doc.Result[key], &list); err != nil {
		return nil, fmt.Errorf("herdr %s list: unreadable reply: %w", noun, err)
	}
	out := map[string]string{}
	for _, e := range list {
		id, _ := e[idKey].(string)
		label, _ := e["label"].(string)
		out[id] = label
	}
	return out, nil
}

// PaneRects reads `herdr pane layout --pane <pane>`.
func (h *herdr) PaneRects(ctx context.Context, pane string) (map[string]Rect, error) {
	r := h.herdr(ctx, "pane", "layout", "--pane", pane)
	if r.ExitCode != 0 {
		return nil, fmt.Errorf("herdr pane layout %s failed: %s", pane, strings.TrimSpace(r.Stderr))
	}
	var doc struct {
		Result struct {
			Layout struct {
				Panes []struct {
					PaneID string `json:"pane_id"`
					Rect   struct {
						X int `json:"x"`
						Y int `json:"y"`
						W int `json:"width"`
						H int `json:"height"`
					} `json:"rect"`
				} `json:"panes"`
			} `json:"layout"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &doc); err != nil {
		return nil, fmt.Errorf("herdr pane layout: unreadable reply: %w", err)
	}
	out := map[string]Rect{}
	for _, p := range doc.Result.Layout.Panes {
		out[p.PaneID] = Rect{p.Rect.X, p.Rect.Y, p.Rect.W, p.Rect.H}
	}
	return out, nil
}

// SplitInto: `herdr pane move --tab --split --target-pane --ratio`. herdr 0.9.3
// reads --ratio as the share the TARGET pane keeps; the moved pane gets the
// rest (probed: target 0.25 of a 120-cell tab left it 30 cells, the moved pane
// 90). A move into the tab the pane is already in changes nothing
// (`reason: same_tab`), and moving a tab's last pane closes that tab. --label
// is not accepted on a split move, only on --new-tab. --no-focus keeps the
// user's keyboard where it was.
func (h *herdr) SplitInto(ctx context.Context, pane, tab, target, dir string, keep float64) error {
	r := h.herdr(ctx, "pane", "move", pane, "--tab", tab, "--split", dir,
		"--target-pane", target, "--ratio", FormatRatio(keep), "--no-focus")
	if r.ExitCode != 0 {
		return fmt.Errorf("herdr pane move %s failed: %s", pane, strings.TrimSpace(r.Stderr))
	}
	return nil
}

// ToNewTab: `herdr pane move --new-tab --label`. The pane keeps its id, its
// agent name and its process; the tab is new, so its id is not the slot's
// recorded handle any more. The tab goes last in the tab bar.
func (h *herdr) ToNewTab(ctx context.Context, pane, label string) error {
	r := h.herdr(ctx, "pane", "move", pane, "--new-tab", "--label", label, "--no-focus")
	if r.ExitCode != 0 {
		return fmt.Errorf("herdr pane move %s failed: %s", pane, strings.TrimSpace(r.Stderr))
	}
	return nil
}

// RenameTab: `herdr tab rename <tab> <label>`.
func (h *herdr) RenameTab(ctx context.Context, tab, label string) error {
	r := h.herdr(ctx, "tab", "rename", tab, label)
	if r.ExitCode != 0 {
		return fmt.Errorf("herdr tab rename %s failed: %s", tab, strings.TrimSpace(r.Stderr))
	}
	return nil
}

// RenamePane: `herdr pane rename <pane> <label>`.
func (h *herdr) RenamePane(ctx context.Context, pane, label string) error {
	r := h.herdr(ctx, "pane", "rename", pane, label)
	if r.ExitCode != 0 {
		return fmt.Errorf("herdr pane rename %s failed: %s", pane, strings.TrimSpace(r.Stderr))
	}
	return nil
}

// FormatRatio prints a share with at most four decimals: 0.5, 0.3333.
func FormatRatio(f float64) string {
	return strconv.FormatFloat(math.Round(f*1e4)/1e4, 'f', -1, 64)
}
