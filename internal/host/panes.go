package host

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
)

// D3 (#67): the pane-addressed side of a host. Host.Send and Host.Capture are
// slot-addressed; the usage-limit watcher also needs the orchestrator's own
// pane, which is no slot, so it speaks in pane ids: a herdr pane id
// (`w1:p3`, from HERDR_PANE_ID or an agent's pane_id) or a tmux target (a
// `%3` pane id from TMUX_PANE, or a slot's window target).

// PaneHost is the optional pane-addressed side of a Host.
type PaneHost interface {
	// PaneOf resolves a slot's handle to the pane that runs its session, ""
	// when it has none.
	PaneOf(ctx context.Context, slot, handle string) string
	// CapturePane returns recent text of a pane, "" when unreadable.
	CapturePane(ctx context.Context, pane string, lines int) string
	// SendPane types text into the pane and submits it. It does not wait for
	// the session to pick the text up.
	SendPane(ctx context.Context, pane, text string) error
}

// OutputMatch is a pane whose text matched the watched pattern: the matched
// line and the pane text the host read at that moment.
type OutputMatch struct {
	Pane, Line, Text string
}

// OutputWatcher is the optional text-event side of a Host (herdr 0.9.x
// `pane.output_matched`). tmux has none, and its caller captures the pane.
type OutputWatcher interface {
	// WatchOutput subscribes to every pane for the regex. A match that
	// already exists when the subscription starts is delivered by Next too.
	// It fails when herdr cannot be reached, is not 0.9.x, or rejects the
	// subscription (a regex its dialect does not accept among them).
	WatchOutput(ctx context.Context, panes []string, regex string) (OutputWatch, error)
}

// OutputWatch is a live output subscription.
type OutputWatch interface {
	// Next blocks until a watched pane matches. It fails when ctx ends or
	// the event stream closes.
	Next(ctx context.Context) (OutputMatch, error)
	Close()
}

// ---- herdr -----------------------------------------------------------------

// PaneOf asks herdr for the pane of the slot's agent.
func (h *herdr) PaneOf(ctx context.Context, slot, handle string) string {
	if handle == "" {
		return ""
	}
	return jget(h.herdr(ctx, "agent", "get", AgentName(slot, handle)).Stdout, "result.agent.pane_id")
}

// call is one request over the API socket and its single reply line.
func (h *herdr) call(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	sock := h.d.Getenv("HERDR_SOCKET_PATH")
	if sock == "" {
		return nil, errors.New("HERDR_SOCKET_PATH is not set: run from inside a herdr pane")
	}
	conn, err := h.d.Dial(ctx, sock)
	if err != nil {
		return nil, fmt.Errorf("herdr socket: %w", err)
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	req, _ := json.Marshal(map[string]any{"id": "rota-" + strings.ReplaceAll(method, ".", "-"), "method": method, "params": params})
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return nil, fmt.Errorf("herdr socket: %w", err)
	}
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	if !sc.Scan() {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("herdr closed the socket without a reply")
	}
	var f struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(sc.Bytes(), &f); err != nil {
		return nil, fmt.Errorf("herdr socket: unreadable reply: %w", err)
	}
	if f.Error != nil {
		return nil, fmt.Errorf("herdr %s: %s", f.Error.Code, f.Error.Message)
	}
	return f.Result, nil
}

// CapturePane reads the pane's recent text with soft wraps joined.
func (h *herdr) CapturePane(ctx context.Context, pane string, lines int) string {
	res, err := h.call(ctx, "pane.read", map[string]any{"pane_id": pane, "source": "recent_unwrapped", "lines": lines})
	if err != nil {
		return ""
	}
	var r struct {
		Read struct {
			Text string `json:"text"`
		} `json:"read"`
	}
	if json.Unmarshal(res, &r) != nil {
		return ""
	}
	return r.Read.Text
}

// SendPane types the text and presses enter in one write.
func (h *herdr) SendPane(ctx context.Context, pane, text string) error {
	_, err := h.call(ctx, "pane.send_input", map[string]any{"pane_id": pane, "text": text, "keys": []string{"enter"}})
	return err
}

// outFrame is one socket line of an output subscription: an error reply, the
// subscribe reply (a plain ack, or an `output_matched` result when a pane
// already matches), or a `pane.output_matched` event.
type outFrame struct {
	Event string `json:"event"`
	Data  struct {
		PaneID      string `json:"pane_id"`
		MatchedLine string `json:"matched_line"`
		Read        struct {
			Text string `json:"text"`
		} `json:"read"`
	} `json:"data"`
	Result *struct {
		Type        string `json:"type"`
		PaneID      string `json:"pane_id"`
		MatchedLine string `json:"matched_line"`
		Read        struct {
			Text string `json:"text"`
		} `json:"read"`
	} `json:"result"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type outMsg struct {
	m   OutputMatch
	err error
	ack bool
}

type herdrOutput struct {
	conn net.Conn
	ch   chan outMsg
	done chan struct{}
	once sync.Once
}

// WatchOutput implements OutputWatcher: one `events.subscribe` request with a
// `pane.output_matched` subscription per pane, source `recent`, the same
// regex for all. The reply is read before returning, so a refused regex fails
// here and the caller can fall back to capturing the pane.
func (h *herdr) WatchOutput(ctx context.Context, panes []string, regex string) (OutputWatch, error) {
	sock, err := h.socket(ctx)
	if err != nil {
		return nil, err
	}
	if len(panes) == 0 {
		return &herdrOutput{ch: make(chan outMsg), done: make(chan struct{})}, nil
	}
	subs := make([]map[string]any, 0, len(panes))
	for _, p := range panes {
		subs = append(subs, map[string]any{
			"type": "pane.output_matched", "pane_id": p, "source": "recent",
			"match": map[string]string{"type": "regex", "value": regex},
		})
	}
	conn, err := h.d.Dial(ctx, sock)
	if err != nil {
		return nil, fmt.Errorf("herdr socket: %w", err)
	}
	req, _ := json.Marshal(map[string]any{
		"id": "rota-limit-watch", "method": "events.subscribe",
		"params": map[string]any{"subscriptions": subs},
	})
	if _, err := conn.Write(append(req, '\n')); err != nil {
		conn.Close()
		return nil, fmt.Errorf("herdr socket: %w", err)
	}
	w := &herdrOutput{conn: conn, ch: make(chan outMsg, 32), done: make(chan struct{})}
	go w.read()
	select {
	case m := <-w.ch:
		if m.err != nil {
			w.Close()
			return nil, m.err
		}
		if !m.ack {
			// the reply was itself a match: keep it for the first Next
			select {
			case w.ch <- m:
			default:
			}
		}
	case <-ctx.Done():
		w.Close()
		return nil, ctx.Err()
	}
	return w, nil
}

func (w *herdrOutput) read() {
	sc := bufio.NewScanner(w.conn)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	send := func(m outMsg) bool {
		select {
		case w.ch <- m:
			return true
		case <-w.done:
			return false
		}
	}
	acked := false
	for sc.Scan() {
		var f outFrame
		if json.Unmarshal(sc.Bytes(), &f) != nil {
			continue
		}
		if f.Error != nil {
			send(outMsg{err: fmt.Errorf("herdr %s: %s", f.Error.Code, f.Error.Message)})
			return
		}
		if f.Event == "" && f.Result != nil {
			// the subscribe reply
			if f.Result.Type == "output_matched" {
				acked = true
				if !send(outMsg{m: OutputMatch{Pane: f.Result.PaneID, Line: f.Result.MatchedLine, Text: f.Result.Read.Text}}) {
					return
				}
				continue
			}
			if !acked {
				acked = true
				if !send(outMsg{ack: true}) {
					return
				}
			}
			continue
		}
		if f.Event != "pane.output_matched" {
			continue
		}
		if !acked {
			acked = true
			if !send(outMsg{ack: true}) {
				return
			}
		}
		if !send(outMsg{m: OutputMatch{Pane: f.Data.PaneID, Line: f.Data.MatchedLine, Text: f.Data.Read.Text}}) {
			return
		}
	}
	err := sc.Err()
	if err == nil {
		err = errors.New("herdr closed the event stream")
	}
	send(outMsg{err: err})
}

func (w *herdrOutput) Next(ctx context.Context) (OutputMatch, error) {
	for {
		select {
		case m := <-w.ch:
			if m.ack {
				continue
			}
			return m.m, m.err
		case <-ctx.Done():
			return OutputMatch{}, ctx.Err()
		}
	}
}

func (w *herdrOutput) Close() {
	w.once.Do(func() {
		close(w.done)
		if w.conn != nil {
			w.conn.Close()
		}
	})
}

// ---- tmux ------------------------------------------------------------------

// PaneOf: a tmux slot's handle is its window target, which tmux accepts as a
// pane target as it is.
func (t *tmux) PaneOf(ctx context.Context, slot, handle string) string { return handle }

// CapturePane captures the visible pane with soft wraps joined; lines is
// unused, as in Capture.
func (t *tmux) CapturePane(ctx context.Context, pane string, lines int) string {
	if pane == "" {
		return ""
	}
	return t.pane(ctx, pane)
}

// SendPane types the text literally and presses enter.
func (t *tmux) SendPane(ctx context.Context, pane, text string) error {
	if pane == "" {
		return errors.New("no pane to send to")
	}
	if r := t.tmux(ctx, "send-keys", "-t", pane, "-l", "--", text); r.ExitCode != 0 {
		return fmt.Errorf("tmux send-keys failed: %s", strings.TrimSpace(r.Stderr))
	}
	if r := t.tmux(ctx, "send-keys", "-t", pane, "C-m"); r.ExitCode != 0 {
		return fmt.Errorf("tmux send-keys failed: %s", strings.TrimSpace(r.Stderr))
	}
	return nil
}
