package host

// Pane reads, status and turn parsing.
import (
	"context"
	"fmt"
	"github.com/l4ci/rota/internal/strutil"
	"strconv"
	"strings"
)

// ReadPane prints recent pane text with soft wraps joined (the herdr twin of
// capture-pane -J). herdr's own agent_not_found is the proof of a gone pane,
// as in Status; any other failure is just unreadable.
func (h *herdr) ReadPane(ctx context.Context, slot, handle string, lines int) (string, error) {
	if handle == "" {
		return "", nil
	}
	r := h.herdr(ctx, "agent", "read", AgentName(slot, handle), "--source", "recent-unwrapped",
		"--lines", strconv.Itoa(lines), "--format", "text")
	if r.ExitCode != 0 {
		err := fmt.Errorf("herdr agent read failed (exit %d): %s", r.ExitCode, strings.TrimSpace(r.Stderr))
		if jget(r.Stderr, "error.code") == "agent_not_found" {
			err = fmt.Errorf("%w: %w", ErrPaneGone, err)
		}
		return "", err
	}
	return paneText(r), nil
}

// paneText is the pane text of an `agent read` or `pane read` reply. herdr
// prints it as plain text on stdout, not in the JSON envelope its other commands use; a
// failure is a JSON error on stderr and a non-zero exit, read here as no text.
func paneText(r Result) string {
	if r.ExitCode != 0 {
		return ""
	}
	return r.Stdout
}

// Status is herdr's native agent state, or `gone` when the slot has a tab but
// no agent in it (the session exited back to a shell). A never-dispatched
// slot yields the zero status.
func (h *herdr) Status(ctx context.Context, slot, handle string) AgentStatus {
	if handle == "" {
		return AgentStatus{}
	}
	return h.statusOf(ctx, AgentName(slot, handle))
}

func (h *herdr) statusOf(ctx context.Context, name string) AgentStatus {
	r := h.herdr(ctx, "agent", "get", name)
	if r.ExitCode != 0 {
		return failedStatus(r)
	}
	return AgentStatus{State: jget(r.Stdout, "result.agent.agent_status")}
}

// failedStatus reads a failed `agent get`. herdr's own agent_not_found is the
// one positive proof the session is gone. A tab or pane the handle no longer
// names is a stale handle, and anything else (no server, a socket error, a
// binary that cannot run) is no answer at all: neither says the agent died.
func failedStatus(r Result) AgentStatus {
	switch jget(r.Stderr, "error.code") {
	case "agent_not_found":
		return AgentStatus{State: StatusGone}
	case "tab_not_found", "pane_not_found", "agent_pane_not_found":
		return AgentStatus{State: StatusUnverifiable, Reason: ReasonStaleHandle}
	}
	return AgentStatus{State: StatusUnverifiable, Reason: ReasonHostUnreachable}
}

// Turn reads the slot's agent from `agent get`: agent_status, state_change_seq
// and completion_seq. herdr sets completion_seq only while the agent rests
// after completed work, so it is 0 while working and after startup; a herdr
// older than 0.9.3 omits both numbers.
func (h *herdr) Turn(ctx context.Context, slot, handle string) (Turn, bool) {
	if handle == "" {
		return Turn{}, false
	}
	r := h.herdr(ctx, "agent", "get", AgentName(slot, handle))
	if r.ExitCode != 0 {
		return Turn{}, false
	}
	num := func(key string) int {
		n, _ := strconv.Atoi(jget(r.Stdout, "result.agent."+key))
		return n
	}
	return Turn{Status: jget(r.Stdout, "result.agent.agent_status"),
		StateSeq: num("state_change_seq"), CompletionSeq: num("completion_seq")}, true
}

// explainLines bounds the fallback excerpt of `herdr agent explain`.
const explainLines = 3

// explainKeys are the lines of `herdr agent explain` that say why herdr
// classified the agent as it did, in the order kept. `agent:` and `manifest:`
// only identify it.
var explainKeys = []string{"state:", "rule:", "evidence:"}

// Explain is the why-lines of `herdr agent explain` for the slot's agent
// (state, rule, evidence, each clipped), the first few lines when none of
// those keys appear, "" for a never-dispatched slot or a failing call.
func (h *herdr) Explain(ctx context.Context, slot, handle string) string {
	if handle == "" {
		return ""
	}
	r := h.herdr(ctx, "agent", "explain", AgentName(slot, handle))
	if r.ExitCode != 0 {
		return ""
	}
	var lines []string
	for _, l := range strings.Split(r.Stdout, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	var out []string
	for _, k := range explainKeys {
		for _, l := range lines {
			if strings.HasPrefix(l, k) {
				out = append(out, strutil.Clip(l, 80))
				break
			}
		}
	}
	if len(out) == 0 {
		for _, l := range lines[:min(len(lines), explainLines)] {
			out = append(out, strutil.Clip(l, 80))
		}
	}
	return strings.Join(out, " / ")
}
