package host

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Tab is one host tab with the proof `rota reap` needs before it may close it.
type Tab struct {
	ID   string
	Cwds []string // the working directory of each pane
	// Agentless is true only when the host can prove no agent runs in the
	// tab: no pane reports an agent, and every pane's foreground processes
	// are plain shells. Anything unreadable or unknown is false.
	Agentless bool
}

// TabLister is the optional view of a host's tabs and the way to close one.
// Only herdr has it: a tmux window carries no agent state, so nothing about a
// window can prove its agent dead.
type TabLister interface {
	Tabs(ctx context.Context) ([]Tab, error)
	// CloseTab closes one tab and proves it gone.
	CloseTab(ctx context.Context, id string) error
}

var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "fish": true, "dash": true, "ksh": true, "nu": true}

// Tabs reads `herdr pane list` and, for each pane that reports no agent,
// `herdr pane process-info`.
func (h *herdr) Tabs(ctx context.Context) ([]Tab, error) {
	r := h.herdr(ctx, "pane", "list")
	if r.ExitCode != 0 {
		return nil, fmt.Errorf("herdr pane list failed: %s", strings.TrimSpace(r.Stderr))
	}
	var doc struct {
		Result struct {
			Panes []struct {
				PaneID string `json:"pane_id"`
				TabID  string `json:"tab_id"`
				Cwd    string `json:"cwd"`
				Agent  string `json:"agent"`
				Status string `json:"agent_status"`
			} `json:"panes"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &doc); err != nil {
		return nil, fmt.Errorf("herdr pane list: unreadable reply: %w", err)
	}
	var order []string
	byID := map[string]*Tab{}
	for _, p := range doc.Result.Panes {
		t := byID[p.TabID]
		if t == nil {
			t = &Tab{ID: p.TabID, Agentless: true}
			byID[p.TabID] = t
			order = append(order, p.TabID)
		}
		t.Cwds = append(t.Cwds, p.Cwd)
		if !t.Agentless {
			continue
		}
		if p.Agent != "" || (p.Status != "unknown" && p.Status != "") || !h.onlyShells(ctx, p.PaneID) {
			t.Agentless = false
		}
	}
	out := make([]Tab, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out, nil
}

// onlyShells is true when the pane's foreground processes were read and all
// are shells. A reply that cannot be read proves nothing.
func (h *herdr) onlyShells(ctx context.Context, pane string) bool {
	r := h.herdr(ctx, "pane", "process-info", "--pane", pane)
	if r.ExitCode != 0 {
		return false
	}
	var v struct {
		Result struct {
			Info struct {
				Fg []struct {
					Name string `json:"name"`
				} `json:"foreground_processes"`
			} `json:"process_info"`
		} `json:"result"`
	}
	if json.Unmarshal([]byte(r.Stdout), &v) != nil || len(v.Result.Info.Fg) == 0 {
		return false
	}
	for _, p := range v.Result.Info.Fg {
		if !shells[baseName(p.Name)] {
			return false
		}
	}
	return true
}

// CloseTab closes the tab and fails unless `tab get` then fails (tab ids are
// never reused, so that is unambiguous).
func (h *herdr) CloseTab(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("no tab id")
	}
	if r := h.herdr(ctx, "tab", "close", id); r.ExitCode != 0 {
		return fmt.Errorf("herdr tab close %s failed: %s", id, strings.TrimSpace(r.Stderr))
	}
	if h.herdr(ctx, "tab", "get", id).ExitCode == 0 {
		return fmt.Errorf("tab %s is still open after close", id)
	}
	return nil
}

// PaneSweeper is the optional way to close a slot's leftover shell panes by
// working directory. A worker that exits leaves its pane behind as a bare
// shell, and `layout split` moves panes out of their tab, so the tab handle
// no longer finds them. Only herdr has it.
type PaneSweeper interface {
	// SweepShells closes every pane in the cwd that runs no agent and only
	// shells, and returns how many it closed. A pane it cannot prove agentless
	// is left alone.
	SweepShells(ctx context.Context, cwd string) (int, error)
}

// SweepShells reads `herdr pane list` and closes each agentless, shell-only
// pane whose cwd is the worktree.
func (h *herdr) SweepShells(ctx context.Context, cwd string) (int, error) {
	r := h.herdr(ctx, "pane", "list")
	if r.ExitCode != 0 {
		return 0, fmt.Errorf("herdr pane list failed: %s", strings.TrimSpace(r.Stderr))
	}
	var doc struct {
		Result struct {
			Panes []struct {
				PaneID string `json:"pane_id"`
				Cwd    string `json:"cwd"`
				Agent  string `json:"agent"`
				Status string `json:"agent_status"`
			} `json:"panes"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &doc); err != nil {
		return 0, fmt.Errorf("herdr pane list: unreadable reply: %w", err)
	}
	n := 0
	for _, p := range doc.Result.Panes {
		if p.Cwd != cwd || p.Agent != "" || (p.Status != "unknown" && p.Status != "") || !h.onlyShells(ctx, p.PaneID) {
			continue
		}
		if h.herdr(ctx, "pane", "close", p.PaneID).ExitCode == 0 {
			n++
		}
	}
	return n, nil
}
