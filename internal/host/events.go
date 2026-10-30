package host

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// SupportedHerdr is the herdr minor `rota round wait` is written against.
// herdr is pre-1.0 and its socket API moves between minors, so any other
// minor is refused instead of guessed at. Pinned to 0.9.x (0.9.3 was the
// protocol 22 schema this was built from). A var so a test can move the pin.
var SupportedHerdr = "0.9"

// ErrUnsupportedHerdr: the installed herdr is not a SupportedHerdr release.
var ErrUnsupportedHerdr = errors.New("unsupported herdr version")

// ErrStreamLost: the event stream ended without the caller asking (herdr sent
// `events_lost`, closed the socket, or restarted). herdr replays nothing, so
// the caller re-subscribes and re-reads current state.
var ErrStreamLost = errors.New("herdr event stream lost")

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
	// slot. It fails when ctx ends, or with ErrStreamLost when the host's
	// event stream ends.
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
		return &herdrWatch{}, nil
	}
	st, err := h.subscribeAt(ctx, sock, "rota-round-wait", subs, 16)
	if err != nil {
		return nil, err
	}
	return &herdrWatch{st: st, panes: panes}, nil
}

type herdrWatch struct {
	st    *stream // nil when no watched slot has a pane
	panes map[string]string
}

// Next returns the slot of the next status change of a watched pane. The
// socket carries only what was subscribed to; the pane check is a second
// filter in case the server ever widens a subscription.
func (w *herdrWatch) Next(ctx context.Context) (string, error) {
	if w.st == nil {
		<-ctx.Done()
		return "", ctx.Err()
	}
	for {
		f, err := w.st.next(ctx)
		if err != nil {
			return "", err
		}
		if f.Event != "pane.agent_status_changed" && f.Event != "pane_agent_status_changed" {
			continue
		}
		if slot, ok := w.panes[f.Data.PaneID]; ok {
			return slot, nil
		}
	}
}

func (w *herdrWatch) Close() {
	if w.st != nil {
		w.st.Close()
	}
}
