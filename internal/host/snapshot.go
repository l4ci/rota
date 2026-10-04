package host

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Agent is one live session a host reports. Tab is the handle a slot records
// (herdr tab id, tmux `<session>:<window>`), Cwd the directory it runs in.
type Agent struct {
	Tab    string
	Name   string
	Cwd    string
	Status string // the host's native state; "" when it has none (tmux)
}

// Snapshotter is the optional read-only view of everything a host runs.
// `rota round` uses it to find tabs no slot owns; it is separate from Host so
// the Host interface stays as the worker verbs know it.
type Snapshotter interface {
	Snapshot(ctx context.Context) ([]Agent, error)
}

// Snapshot reads `herdr api snapshot` (herdr 0.9.x): the `agents` array of
// the live session. A pane whose agent has exited back to a shell is not in
// it, which is how a dead slot shows.
func (h *herdr) Snapshot(ctx context.Context) ([]Agent, error) {
	r := h.herdr(ctx, "api", "snapshot")
	if r.ExitCode != 0 {
		return nil, fmt.Errorf("herdr api snapshot failed: %s", strings.TrimSpace(r.Stderr))
	}
	var doc struct {
		Result struct {
			Snapshot struct {
				Agents []struct {
					Name   string `json:"name"`
					TabID  string `json:"tab_id"`
					Cwd    string `json:"cwd"`
					Status string `json:"agent_status"`
				} `json:"agents"`
			} `json:"snapshot"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &doc); err != nil {
		return nil, fmt.Errorf("herdr api snapshot: unreadable reply: %w", err)
	}
	out := []Agent{}
	for _, a := range doc.Result.Snapshot.Agents {
		out = append(out, Agent{Tab: a.TabID, Name: a.Name, Cwd: a.Cwd, Status: a.Status})
	}
	return out, nil
}

// Snapshot lists every tmux window of every session with its pane's path.
func (t *tmux) Snapshot(ctx context.Context) ([]Agent, error) {
	r := t.tmux(ctx, "list-windows", "-a", "-F", "#{session_name}:#{window_name}\t#{pane_current_path}")
	if r.ExitCode != 0 {
		return nil, fmt.Errorf("tmux list-windows failed: %s", strings.TrimSpace(r.Stderr))
	}
	out := []Agent{}
	for _, line := range strings.Split(r.Stdout, "\n") {
		tab, cwd, ok := strings.Cut(line, "\t")
		if !ok || tab == "" {
			continue
		}
		out = append(out, Agent{Tab: tab, Cwd: cwd})
	}
	return out, nil
}
