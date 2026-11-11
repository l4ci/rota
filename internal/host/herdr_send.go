package host

// Send path: typed relay, submit and queued-relay state machine.
import (
	"context"
	"github.com/l4ci/rota/internal/harness"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Send submits the file and confirms pickup: it returns once the agent is
// observed working (or blocked on a dialog), NOT when the task finishes,
// since a plain --wait would hold until the whole task settled. ErrQueued
// acknowledges a relay in a busy Codex session. ErrNotSubmitted
// means no activity followed the submission; ErrDialogOpen means a dialog was
// already up and nothing was sent; ErrDraftOnPrompt means a human draft is on
// the prompt line and nothing was typed.
//
// herdr can type the brief and lose the Enter (#89). A brief left on the
// prompt line is submitted with bounded Enter retries instead of failing.
func (h *herdr) Send(ctx context.Context, slot, handle, file string) error {
	text, err := h.d.readBrief(file)
	if err != nil {
		return err
	}
	text = strings.TrimRight(text, "\n") // the shell host read it with $(cat)
	name := AgentName(slot, handle)
	read := func() string { return h.draftPane(ctx, name) }
	if err := clearDialog(&h.d, read, func(k string) { h.herdr(ctx, "agent", "send-keys", name, k) }); err != nil {
		return err
	}
	// A human typing in the pane would get the brief mixed into their draft.
	if waitNoDraft(&h.d, read, text) != "" {
		return ErrDraftOnPrompt
	}
	r := h.herdr(ctx, "agent", "prompt", name, text,
		"--wait", "--until", "working", "--until", "blocked", "--timeout", "60000")
	status, kind := jget(r.Stdout, "result.agent.agent_status"), jget(r.Stdout, "result.agent.agent")
	confirm := r.ExitCode == 0
	// A busy Codex can receive the relay while the prompt RPC times out
	// waiting for a new working transition. Its pane still proves delivery.
	// Keep other harnesses and an explicit input refusal on their old path.
	if !confirm && hasRelayMarker(text) && jget(r.Stderr, "error.code") != "agent_blocked" {
		agent := h.herdr(ctx, "agent", "get", name)
		kind = jget(agent.Stdout, "result.agent.agent")
		if hz, ok := harness.LookupPane(kind); agent.ExitCode == 0 && ok && hz.PromptAcknowledgement().RequireEcho {
			status = jget(agent.Stdout, "result.agent.agent_status")
			confirm = true
		}
	}
	if confirm {
		if hz, known := harness.LookupPane(kind); known && kind != "" && !hz.PromptAcknowledgement().RequireEcho && (status == "working" || status == "blocked") {
			return nil
		}
		pane := h.promptPane(ctx, name)
		// A harness can remain at its empty startup prompt even when herdr's
		// prompt RPC reports success (including a transient working status).
		// Only an echo above the input line acknowledges a finished turn.
		output := pane
		if i := strings.LastIndexAny(pane, promptMarkers); i >= 0 {
			output = pane[:i]
		}
		ack := briefTail(text) != "" && strings.Contains(squeeze(output), briefTail(text))
		if (status == "working" || status == "blocked") && harness.RequiresPromptEcho(kind, pane) && hasRelayMarker(text) && relayQueued(output, text) {
			return ErrQueued
		}
		if ack || ((status == "working" || status == "blocked") && !harness.RequiresPromptEcho(kind, pane)) {
			return nil
		}
		// A busy agent queues the relay in a preview above the composer, which
		// can be cut short of the brief's tail. The relay's own marker above
		// the prompt line shows it arrived (#1862); on the prompt line it is a
		// brief whose Enter was lost, and falls through to the resubmit.
		if (status == "working" || status == "blocked") && relayQueued(output, text) {
			return nil
		}
	}
	if jget(r.Stderr, "error.code") == "agent_blocked" {
		return ErrDialogOpen
	}
	if h.briefOnPrompt(ctx, name, text) {
		return h.submitTyped(ctx, name)
	}
	return ErrNotSubmitted
}

func hasRelayMarker(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if harness.RelaySequence(line) != "" {
			return true
		}
	}
	return false
}

// relayQueued reports whether output, the pane text above the prompt line,
// shows this relay: its opening-line marker, or the brief's tail.
func relayQueued(output, text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if seq := harness.RelaySequence(line); seq != "" {
			for _, echoed := range strings.Split(output, "\n") {
				if harness.RelaySequence(echoed) == seq {
					return true
				}
			}
			break // only this relay's marker can acknowledge pickup
		}
	}
	tail := briefTail(text)
	return tail != "" && strings.Contains(squeeze(output), tail)
}

// SubmitPending is the resend path: when the file's text is already on the
// prompt line (a prior Send typed it and its Enters were lost), it submits that
// text and reports handled, so the resend never types a second copy on top.
// Not handled means the prompt line holds no such brief and Send applies.
func (h *herdr) SubmitPending(ctx context.Context, slot, handle, file string) (bool, error) {
	text, err := h.d.readFile(file)
	if err != nil {
		return false, nil
	}
	name := AgentName(slot, handle)
	if h.Status(ctx, slot, handle) != "idle" || !h.briefOnPrompt(ctx, name, strings.TrimRight(text, "\n")) {
		return false, nil
	}
	return true, h.submitTyped(ctx, name)
}

// pastePlaceholder is how Claude Code shows a long paste on the prompt line.
var pastePlaceholder = regexp.MustCompile(`\[Pasted text #\d+( \+\d+ lines)?\]`)

// briefOnPrompt reports whether the brief sits unsent on the prompt line: the
// text after the pane's last prompt marker holds either the tail of text or a
// paste placeholder. The tail is the brief's last line: it is typed last, so it
// is there only when the whole brief is. Scrollback above the marker is a brief
// already sent and never counts. Whitespace is dropped on both sides so soft
// wraps do not matter.
func (h *herdr) briefOnPrompt(ctx context.Context, name, text string) bool {
	pane := h.promptPane(ctx, name)
	i := strings.LastIndexAny(pane, promptMarkers)
	if i < 0 {
		return false
	}
	_, size := utf8.DecodeRuneInString(pane[i:])
	prompt := pane[i+size:]
	if pastePlaceholder.MatchString(prompt) {
		return true
	}
	tail := briefTail(text)
	return tail != "" && strings.Contains(squeeze(prompt), tail)
}

// promptPane is the recent pane text the prompt line is read from.
func (h *herdr) promptPane(ctx context.Context, name string) string {
	return paneText(h.herdr(ctx, "agent", "read", name, "--source", "recent-unwrapped",
		"--lines", "60", "--format", "text"))
}

// draftPane reads the pane with styling and drops the dim runs, so a ghost
// suggestion is not mistaken for typed text. A failed or empty styled read
// falls back to the plain text.
func (h *herdr) draftPane(ctx context.Context, name string) string {
	r := h.herdr(ctx, "agent", "read", name, "--source", "recent-unwrapped",
		"--lines", "60", "--format", "ansi")
	if r.ExitCode == 0 && strings.TrimSpace(r.Stdout) != "" {
		return stripDim(r.Stdout)
	}
	return h.promptPane(ctx, name)
}

func squeeze(s string) string {
	return strings.Join(strings.Fields(s), "")
}

// submitTyped confirms a brief already on the prompt line, pressing Enter
// only while idle. Retry bounds and wait policy live in pickup.go.
func (h *herdr) submitTyped(ctx context.Context, name string) error {
	return confirmPickup(&h.d, Herdr, func(timeout time.Duration) (bool, error) {
		var status string
		if timeout == 0 {
			// Enter only into an idle prompt: on a dialog it would answer
			// the dialog, which nobody approved.
			status = h.statusOf(ctx, name)
		} else {
			w := h.herdr(ctx, "agent", "wait", name, "--until", "working", "--until", "blocked",
				"--timeout", strconv.FormatInt(timeout.Milliseconds(), 10))
			status = jget(w.Stdout, "result.agent.agent_status")
		}
		switch status {
		case "working":
			return true, nil
		case "blocked":
			return false, ErrDialogOpen
		case "idle":
		default:
			if timeout == 0 {
				return false, ErrNotSubmitted
			}
		}
		return false, nil
	}, func() {
		h.herdr(ctx, "agent", "send-keys", name, "enter")
	})
}
