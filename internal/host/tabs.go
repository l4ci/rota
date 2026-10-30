package host

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
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
// Herdr and Orca provide it. A tmux window carries no agent state, so nothing
// about a window can prove its agent dead.
type TabLister interface {
	Tabs(ctx context.Context) ([]Tab, error)
	// CloseTab closes one tab and proves it gone.
	CloseTab(ctx context.Context, id string) error
}

var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "fish": true, "dash": true, "ksh": true, "nu": true}

// Tabs reads `herdr pane list` and, for each pane that reports no agent,
// `herdr pane process-info`.
func (h *herdr) Tabs(ctx context.Context) ([]Tab, error) {
	panes, err := h.listPanes(ctx)
	if err != nil {
		return nil, err
	}
	var order []string
	byID := map[string]*Tab{}
	for _, p := range panes {
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
		if !h.agentless(ctx, p) {
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
	panes, err := h.listPanes(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, p := range panes {
		if p.Cwd != cwd || !h.agentless(ctx, p) {
			continue
		}
		if h.herdr(ctx, "pane", "close", p.PaneID).ExitCode == 0 {
			n++
		}
	}
	return n, nil
}

// HandleFor is the handle a slot records for an agent in tab, pane: the tab
// when the agent is its only pane, else the pane, which a kill closes alone.
// A count that was not read (0) is the safe side too: the pane.
func HandleFor(tab, pane string, tabPanes int) string {
	if pane != "" && tabPanes != 1 {
		return pane
	}
	return tab
}

// isPaneHandle: herdr ids are `w1:t7` for a tab and `w1:p3` for a pane.
func isPaneHandle(handle string) bool { return strings.Contains(handle, ":p") }

// TabFollower is the optional way to keep a slot's handle where its pane is.
// Only herdr has it: its agent name derives from the handle, and a pane moved
// to another tab keeps the name it was started under.
type TabFollower interface {
	// CurrentHandle is the handle the slot's agent has now (see HandleFor),
	// "" when the host cannot say.
	CurrentHandle(ctx context.Context, slot, handle string) string
	// FollowTab renames the slot's agent for its new handle, so lookups by
	// the new handle find it.
	FollowTab(ctx context.Context, slot, from, to string) error
}

// CurrentHandle reads `agent get` and `tab get`: the agent is addressed by the
// name it was started under, which a move between tabs does not change.
func (h *herdr) CurrentHandle(ctx context.Context, slot, handle string) string {
	if handle == "" {
		return ""
	}
	r := h.herdr(ctx, "agent", "get", AgentName(slot, handle))
	if r.ExitCode != 0 {
		return ""
	}
	tab, pane := jget(r.Stdout, "result.agent.tab_id"), jget(r.Stdout, "result.agent.pane_id")
	if tab == "" {
		return ""
	}
	n, _, _ := h.tabPanes(ctx, tab)
	return HandleFor(tab, pane, n)
}

// tabPanes is `tab get`'s pane_count. ok is false when it was not read; gone
// says herdr knows no such tab.
func (h *herdr) tabPanes(ctx context.Context, tab string) (n int, gone, ok bool) {
	t := h.herdr(ctx, "tab", "get", tab)
	if t.ExitCode != 0 {
		return 0, jget(t.Stderr, "error.code") == "tab_not_found", false
	}
	n, err := strconv.Atoi(jget(t.Stdout, "result.tab.pane_count"))
	return n, false, err == nil
}

// FollowTab is `herdr agent rename`. It fails when herdr refuses, so a caller
// never records a handle whose agent name nothing answers to.
func (h *herdr) FollowTab(ctx context.Context, slot, from, to string) error {
	r := h.herdr(ctx, "agent", "rename", AgentName(slot, from), AgentName(slot, to))
	if r.ExitCode != 0 {
		return fmt.Errorf("herdr agent rename failed for slot '%s' (%s -> %s): %s", slot, from, to, strings.TrimSpace(r.Stderr))
	}
	return nil
}
