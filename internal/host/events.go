package host

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"sync"
)

// SupportedHerdr is the herdr minor `rota round wait` is written against.
// herdr is pre-1.0 and its socket API moves between minors, so any other
// minor is refused instead of guessed at. Pinned to 0.9.x (0.9.3 was the
// protocol 22 schema this was built from). A var so a test can move the pin.
var SupportedHerdr = "0.9"

// ErrUnsupportedHerdr: the installed herdr is not a SupportedHerdr release.
var ErrUnsupportedHerdr = errors.New("unsupported herdr version")

// WatchTarget is one slot a Watcher wakes for.
type WatchTarget struct{ Slot, Handle string }

// Watcher is the optional event side of a Host. A host without it (tmux has no
// agent status and no event stream) is polled by the caller instead.
type Watcher interface {
	// Watch starts listening for the targets. Anything that happens after it
	// returns is delivered by Next, so a caller that classifies the slots
	// AFTER Watch cannot miss a change in between.
	Watch(ctx context.Context, targets []WatchTarget) (Watch, error)
}

// Watch is a live subscription.
type Watch interface {
	// Next blocks until a watched slot's agent status changes and returns the
	// slot. It fails when ctx ends or the host's event stream closes.
	Next(ctx context.Context) (slot string, err error)
	Close()
}

var reHerdrVersion = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// HerdrVersion reads the version out of `herdr --version` output. The version
// is "" when none is readable; a readable one that is not a SupportedHerdr
// release comes back with ErrUnsupportedHerdr.
func HerdrVersion(out string) (string, error) {
	m := reHerdrVersion.FindStringSubmatch(out)
	if m == nil {
		return "", fmt.Errorf("%w: cannot read a version from %q", ErrUnsupportedHerdr, strings.TrimSpace(out))
	}
	v := m[0]
	if m[1]+"."+m[2] != SupportedHerdr {
		return v, fmt.Errorf("%w: herdr %s, rota round wait supports %s.x", ErrUnsupportedHerdr, v, SupportedHerdr)
	}
	return v, nil
}

// checkHerdrVersion accepts SupportedHerdr output only.
func checkHerdrVersion(out string) error {
	_, err := HerdrVersion(out)
	return err
}

// socket checks the installed herdr is a SupportedHerdr release and returns
// its API socket path.
func (h *herdr) socket(ctx context.Context) (string, error) {
	r := h.herdr(ctx, "--version")
	if r.ExitCode != 0 {
		return "", fmt.Errorf("herdr --version failed: %s", strings.TrimSpace(r.Stderr))
	}
	if err := checkHerdrVersion(r.Stdout); err != nil {
		return "", err
	}
	sock := h.d.Getenv("HERDR_SOCKET_PATH")
	if sock == "" {
		return "", errors.New("HERDR_SOCKET_PATH is not set: run from inside a herdr pane")
	}
	return sock, nil
}

// Watch implements Watcher over herdr's socket API: one `events.subscribe`
// request carrying a `pane.agent_status_changed` subscription per watched
// pane. The CLI's `herdr agent wait` takes one target, so waiting for the
// first of N slots through it would need N child processes.
func (h *herdr) Watch(ctx context.Context, targets []WatchTarget) (Watch, error) {
	sock, err := h.socket(ctx)
	if err != nil {
		return nil, err
	}
	// A slot's handle is its tab; events carry pane ids. A slot whose agent is
	// gone has no pane to watch, and the caller's classification reports it.
	panes := map[string]string{}
	var subs []map[string]string
	for _, t := range targets {
		pane := jget(h.herdr(ctx, "agent", "get", AgentName(t.Slot, t.Handle)).Stdout, "result.agent.pane_id")
		if pane == "" {
			continue
		}
		panes[pane] = t.Slot
		subs = append(subs, map[string]string{"type": "pane.agent_status_changed", "pane_id": pane})
	}
	if len(subs) == 0 {
		return &herdrWatch{ch: make(chan watchMsg), done: make(chan struct{})}, nil
	}
	conn, err := h.d.Dial(ctx, sock)
	if err != nil {
		return nil, fmt.Errorf("herdr socket: %w", err)
	}
	req, _ := json.Marshal(map[string]any{
		"id": "rota-round-wait", "method": "events.subscribe",
		"params": map[string]any{"subscriptions": subs},
	})
	if _, err := conn.Write(append(req, '\n')); err != nil {
		conn.Close()
		return nil, fmt.Errorf("herdr socket: %w", err)
	}
	w := &herdrWatch{conn: conn, panes: panes, ch: make(chan watchMsg, 16), done: make(chan struct{})}
	go w.read()
	// The reply to events.subscribe comes first; failing here beats waiting
	// forever on a subscription that never started.
	select {
	case m := <-w.ch:
		if m.err != nil {
			w.Close()
			return nil, m.err
		}
	case <-ctx.Done():
		w.Close()
		return nil, ctx.Err()
	}
	return w, nil
}

type watchMsg struct {
	slot string
	err  error
}

type herdrWatch struct {
	conn  net.Conn
	panes map[string]string
	ch    chan watchMsg
	done  chan struct{}
	once  sync.Once
}

// frame is one line from the socket: the subscribe reply
// (`{"id":..,"result":{"type":"subscription_started"}}`), an error reply, or
// an event (`{"event":"pane.agent_status_changed","data":{..}}`).
type frame struct {
	Event string `json:"event"`
	Data  struct {
		PaneID string `json:"pane_id"`
	} `json:"data"`
	Result *struct {
		Type string `json:"type"`
	} `json:"result"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// read forwards the first line as an ack or error, then one message per
// status change of a watched pane. The socket carries only what was
// subscribed to; the pane check is a second filter in case the server ever
// widens a subscription.
func (w *herdrWatch) read() {
	sc := bufio.NewScanner(w.conn)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	send := func(m watchMsg) bool {
		select {
		case w.ch <- m:
			return true
		case <-w.done:
			return false
		}
	}
	acked := false
	for sc.Scan() {
		var f frame
		if json.Unmarshal(sc.Bytes(), &f) != nil {
			continue
		}
		if f.Error != nil {
			send(watchMsg{err: fmt.Errorf("herdr %s: %s", f.Error.Code, f.Error.Message)})
			return
		}
		if !acked {
			acked = true
			if !send(watchMsg{}) {
				return
			}
			if f.Event == "" {
				continue
			}
		}
		if f.Event != "pane.agent_status_changed" && f.Event != "pane_agent_status_changed" {
			continue
		}
		if slot, ok := w.panes[f.Data.PaneID]; ok {
			if !send(watchMsg{slot: slot}) {
				return
			}
		}
	}
	err := sc.Err()
	if err == nil {
		err = errors.New("herdr closed the event stream")
	}
	send(watchMsg{err: err})
}

func (w *herdrWatch) Next(ctx context.Context) (string, error) {
	select {
	case m := <-w.ch:
		return m.slot, m.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (w *herdrWatch) Close() {
	w.once.Do(func() {
		close(w.done)
		if w.conn != nil {
			w.conn.Close()
		}
	})
}
