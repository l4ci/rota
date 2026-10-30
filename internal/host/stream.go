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

// socket checks the installed herdr is a SupportedHerdr release and returns
// its API socket path. Every socket call goes through it, so none can talk to
// a herdr whose API this build was not written against.
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

// frame is one line from the socket: the subscribe reply
// (`{"id":..,"result":{"type":"subscription_started"}}`, or an
// `output_matched` result when a pane already matches), an error reply, or an
// event (`{"event":"pane.agent_status_changed","data":{..}}`). It holds the
// fields of every event a watcher reads.
type frame struct {
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

type streamMsg struct {
	f   frame
	err error
}

// stream is one `events.subscribe` connection. It owns dial, the subscribe
// write, the ack, and turning `events_lost`, error replies and a closed
// socket into errors; a watcher only filters the frames that remain.
type stream struct {
	conn  net.Conn
	ch    chan streamMsg
	done  chan struct{}
	once  sync.Once
	first *frame // the subscribe reply, handed to the first next
}

// subscribeAt dials the socket, sends the subscriptions and returns once the
// reply has arrived, so a refused subscription fails here instead of
// blocking a caller forever. The reply is kept for the first next: it can
// carry an event of its own. The caller passes the path socket() checked.
func (h *herdr) subscribeAt(ctx context.Context, sock, id string, subs any, buf int) (*stream, error) {
	conn, err := h.d.Dial(ctx, sock)
	if err != nil {
		return nil, fmt.Errorf("herdr socket: %w", err)
	}
	req, _ := json.Marshal(map[string]any{
		"id": id, "method": "events.subscribe",
		"params": map[string]any{"subscriptions": subs},
	})
	if _, err := conn.Write(append(req, '\n')); err != nil {
		conn.Close()
		return nil, fmt.Errorf("herdr socket: %w", err)
	}
	s := &stream{conn: conn, ch: make(chan streamMsg, buf), done: make(chan struct{})}
	go s.read()
	select {
	case m := <-s.ch:
		if m.err != nil {
			s.Close()
			return nil, m.err
		}
		s.first = &m.f
	case <-ctx.Done():
		s.Close()
		return nil, ctx.Err()
	}
	return s, nil
}

func (s *stream) read() {
	sc := bufio.NewScanner(s.conn)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	send := func(m streamMsg) bool {
		select {
		case s.ch <- m:
			return true
		case <-s.done:
			return false
		}
	}
	for sc.Scan() {
		var f frame
		if json.Unmarshal(sc.Bytes(), &f) != nil {
			continue
		}
		if f.Event == "events_lost" || (f.Error != nil && f.Error.Code == "events_lost") {
			send(streamMsg{err: fmt.Errorf("%w: events_lost", ErrStreamLost)})
			return
		}
		if f.Error != nil {
			send(streamMsg{err: fmt.Errorf("herdr %s: %s", f.Error.Code, f.Error.Message)})
			return
		}
		if !send(streamMsg{f: f}) {
			return
		}
	}
	err := sc.Err()
	if err == nil {
		err = errors.New("herdr closed the event stream")
	}
	send(streamMsg{err: fmt.Errorf("%w: %v", ErrStreamLost, err)})
}

// next blocks for the following frame, the subscribe reply first.
func (s *stream) next(ctx context.Context) (frame, error) {
	if f := s.first; f != nil {
		s.first = nil
		return *f, nil
	}
	select {
	case m := <-s.ch:
		return m.f, m.err
	case <-ctx.Done():
		return frame{}, ctx.Err()
	}
}

func (s *stream) Close() {
	s.once.Do(func() {
		close(s.done)
		s.conn.Close()
	})
}
