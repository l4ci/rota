package host

// Kill, workspace close and process-id parsing.
import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Kill asks the session to exit, closes its tab, and proves it is gone. It
// records the pane's process PIDs first (claude plus its MCP children) and
// fails unless `tab get` reports not-found and every recorded PID has exited. Closed tab
// ids are never reused, so a not-found `tab get` is unambiguous; any other failure proves nothing. `/exit` is a
// client-side command, so there is no wait: it produces no turn to observe;
// it lets Claude Code flush its session first.
func (h *herdr) Kill(ctx context.Context, slot, handle string) error {
	if handle == "" {
		return nil
	}
	name := AgentName(slot, handle)
	var pids []int
	// evidenceUnknown: a failed query before the close means no process
	// evidence was collected, so no later absence can prove cleanup.
	evidenceUnknown := false
	ag := h.herdr(ctx, "agent", "get", name)
	if ag.ExitCode != 0 {
		evidenceUnknown = failedStatus(ag) == UnverifiablePrefix+ReasonHostUnreachable
	}
	pane := jget(ag.Stdout, "result.agent.pane_id")
	if pane != "" {
		pi := h.herdr(ctx, "pane", "process-info", "--pane", pane)
		if pi.ExitCode != 0 && notFound(pi) == presentUnknown {
			evidenceUnknown = true
		}
		pids = processPIDs(pi.Stdout)
	}
	kind, target := "tab", handle
	out, err := runKill(killSteps{
		deps: &h.d, pids: pids, unverifiedBefore: evidenceUnknown,
		close: func() error {
			h.herdr(ctx, "agent", "prompt", name, "/exit")
			// A handle naming a pane is one that shares its tab with panes rota does
			// not own (the orchestrator's): only the pane closes, never that tab.
			paneOnly := isPaneHandle(handle)
			if paneOnly && pane == "" {
				pane = handle
			}
			// A tab handle recorded while the pane was alone goes stale when other
			// panes join that tab. Ask at kill time; a count that cannot be read is
			// not proof the tab is the slot's own.
			if !paneOnly && pane != "" {
				// A tab herdr says is already gone holds nothing to protect.
				if n, gone, ok := h.tabPanes(ctx, handle); !gone && (!ok || n != 1) {
					paneOnly = true
				}
			}
			if !paneOnly {
				h.herdr(ctx, "tab", "close", handle)
			}
			// A `layout split` moves the pane out of its tab, so closing the tab no
			// longer reaches it: close the pane itself.
			if pane != "" {
				h.herdr(ctx, "pane", "close", pane)
			}
			if paneOnly {
				kind, target = "pane", pane
			}
			return nil
		},
		gone: func() presence { return h.probe(ctx, kind, target) },
	})
	if err != nil {
		return err
	}
	return out.err(slot, "herdr", "tab "+handle)
}

// CloseWorkspace implements WorkspaceCloser.
//
// herdr closes a workspace with its last tab, so this is a safety net for one
// left empty. It refuses the caller's own workspace, never closes a workspace
// that still holds a tab, and never passes --group, which would take the
// orchestrator and every sibling slot with it.
func (h *herdr) CloseWorkspace(ctx context.Context, id string) error {
	if id == "" || id == h.d.Getenv("HERDR_WORKSPACE_ID") {
		return fmt.Errorf("refusing to close workspace %q: not a slot workspace", id)
	}
	r := h.herdr(ctx, "workspace", "get", id)
	if r.ExitCode != 0 || jget(r.Stdout, "result.workspace.tab_count") != "0" {
		return nil
	}
	if c := h.herdr(ctx, "workspace", "close", id); c.ExitCode != 0 {
		return fmt.Errorf("herdr workspace close %s failed: %s", id, strings.TrimSpace(c.Stderr))
	}
	return nil
}

// processPIDs reads foreground process pids and the shell pid out of
// `herdr pane process-info`.
func processPIDs(doc string) []int {
	var v struct {
		Result struct {
			Info struct {
				Fg []struct {
					Pid int `json:"pid"`
				} `json:"foreground_processes"`
				Shell int `json:"shell_pid"`
			} `json:"process_info"`
		} `json:"result"`
	}
	if json.Unmarshal([]byte(doc), &v) != nil {
		return nil
	}
	var pids []int
	for _, p := range v.Result.Info.Fg {
		pids = append(pids, p.Pid)
	}
	if v.Result.Info.Shell != 0 {
		pids = append(pids, v.Result.Info.Shell)
	}
	return pids
}
