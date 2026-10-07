package host

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// herdrServer is the far end of the socket: it reads the subscribe request,
// answers it, and plays recorded event lines. Shapes follow `herdr api schema`
// (protocol 22, herdr 0.9.3).
type herdrServer struct {
	conn net.Conn
	req  chan map[string]any
}

func pipeDeps(t *testing.T, version string, panes map[string]string) (Deps, *herdrServer, *fake) {
	t.Helper()
	cli, srv := net.Pipe()
	s := &herdrServer{conn: srv, req: make(chan map[string]any, 1)}
	t.Cleanup(func() { cli.Close(); srv.Close() })
	f := &fake{handler: func(_ string, a []string) Result {
		switch {
		case len(a) == 1 && a[0] == "--version":
			return Result{Stdout: version + "\n"}
		case len(a) >= 3 && a[0] == "agent" && a[1] == "get":
			if p, ok := panes[a[2]]; ok {
				return Result{Stdout: `{"id":"cli","result":{"agent":{"pane_id":"` + p + `","agent_status":"working"}}}`}
			}
			return Result{ExitCode: 1, Stderr: `{"error":{"code":"agent_not_found"}}`}
		}
		return Result{ExitCode: 1}
	}}
	d := deps(f, map[string]string{"HERDR_SOCKET_PATH": "/fake/herdr.sock"}, &clock{})
	d.Dial = func(context.Context, string) (net.Conn, error) { return cli, nil }
	return d, s, f
}

// serve reads the request and writes the ack followed by lines.
func (s *herdrServer) serve(t *testing.T, lines ...string) {
	t.Helper()
	go func() {
		line, err := bufio.NewReader(s.conn).ReadBytes('\n')
		if err != nil {
			return
		}
		var m map[string]any
		json.Unmarshal(line, &m)
		s.req <- m
		for _, l := range lines {
			if _, err := s.conn.Write([]byte(l + "\n")); err != nil {
				return
			}
		}
	}()
}

const ack = `{"id":"rota-round-wait","result":{"type":"subscription_started"}}`

func event(pane, status string) string {
	return `{"event":"pane.agent_status_changed","data":{"pane_id":"` + pane + `","workspace_id":"w1","agent_status":"` + status + `"}}`
}

var targets = []WatchTarget{{"w1", "w1:t1"}, {"w2", "w1:t2"}, {"w3", "w1:t3"}}

func panes() map[string]string {
	return map[string]string{AgentName("w1", "w1:t1"): "w1:p1", AgentName("w2", "w1:t2"): "w1:p2"}
}

func TestWatchSubscribesPerPaneAndFiltersEvents(t *testing.T) {
	d, s, _ := pipeDeps(t, "herdr 0.9.3", panes())
	s.serve(t, ack,
		event("w1:p9", "idle"), // not watched
		`{"event":"pane.scroll_changed","data":{"pane_id":"w1:p1"}}`, // not a status change
		`not json`,
		event("w1:p2", "done"))
	w, err := New("herdr", d).(Watcher).Watch(context.Background(), targets)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	slot, err := w.Next(ctx)
	if err != nil || slot != "w2" {
		t.Fatalf("Next = %q, %v; want w2", slot, err)
	}
	req := <-s.req
	if req["method"] != "events.subscribe" {
		t.Errorf("request = %v", req)
	}
	subs := req["params"].(map[string]any)["subscriptions"].([]any)
	if len(subs) != 2 { // w3's agent is gone: no pane to subscribe to
		t.Fatalf("subscriptions = %v", subs)
	}
	for i, pane := range []string{"w1:p1", "w1:p2"} {
		m := subs[i].(map[string]any)
		if m["type"] != "pane.agent_status_changed" || m["pane_id"] != pane {
			t.Errorf("subscription %d = %v", i, m)
		}
	}
}

func TestWatchRefusesOtherHerdrVersions(t *testing.T) {
	for _, v := range []string{"herdr 0.10.0", "herdr 1.0.0", "herdr 0.8.9", "herdr dev", ""} {
		d, _, f := pipeDeps(t, v, panes())
		_, err := New("herdr", d).(Watcher).Watch(context.Background(), targets)
		if !errors.Is(err, ErrUnsupportedHerdr) {
			t.Errorf("version %q: err = %v", v, err)
		}
		if f.count("herdr agent") != 0 {
			t.Errorf("version %q: touched agents before the version check", v)
		}
	}
	for _, v := range []string{"herdr 0.9.0", "herdr 0.9.12"} {
		if err := checkHerdrVersion(v); err != nil {
			t.Errorf("%q: %v", v, err)
		}
	}
}

func TestWatchNeedsTheSocketPath(t *testing.T) {
	d, _, _ := pipeDeps(t, "herdr 0.9.3", panes())
	d.Getenv = func(string) string { return "" }
	if _, err := New("herdr", d).(Watcher).Watch(context.Background(), targets); err == nil || !strings.Contains(err.Error(), "HERDR_SOCKET_PATH") {
		t.Errorf("err = %v", err)
	}
}

func TestWatchSurfacesAnErrorReply(t *testing.T) {
	d, s, _ := pipeDeps(t, "herdr 0.9.3", panes())
	s.serve(t, `{"id":"rota-round-wait","error":{"code":"invalid_request","message":"bad subscription"}}`)
	_, err := New("herdr", d).(Watcher).Watch(context.Background(), targets)
	if err == nil || !strings.Contains(err.Error(), "bad subscription") {
		t.Errorf("err = %v", err)
	}
}

func TestWatchStreamClosedIsAnError(t *testing.T) {
	d, s, _ := pipeDeps(t, "herdr 0.9.3", panes())
	s.serve(t, ack)
	w, err := New("herdr", d).(Watcher).Watch(context.Background(), targets)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	s.conn.Close() // herdr went away
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := w.Next(ctx); !errors.Is(err, ErrStreamLost) || ctx.Err() != nil {
		t.Errorf("Next = %v, want ErrStreamLost before the deadline", err)
	}
}

func TestWatchEventsLostFrameIsAStreamLoss(t *testing.T) {
	for name, frame := range map[string]string{
		"event": `{"event":"events_lost","data":{}}`,
		"error": `{"error":{"code":"events_lost","message":"subscriber lagged"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			d, s, _ := pipeDeps(t, "herdr 0.9.3", panes())
			s.serve(t, ack, frame)
			w, err := New("herdr", d).(Watcher).Watch(context.Background(), targets)
			if err != nil {
				t.Fatal(err)
			}
			defer w.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := w.Next(ctx); !errors.Is(err, ErrStreamLost) {
				t.Errorf("Next = %v, want ErrStreamLost", err)
			}
		})
	}
}

func TestWatchNextEndsWithItsContext(t *testing.T) {
	d, s, _ := pipeDeps(t, "herdr 0.9.3", panes())
	s.serve(t, ack)
	w, err := New("herdr", d).(Watcher).Watch(context.Background(), targets)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := w.Next(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Next = %v", err)
	}
}

func TestWatchWithNoLivePaneNeverDialsAndBlocksUntilCancelled(t *testing.T) {
	d, _, _ := pipeDeps(t, "herdr 0.9.3", map[string]string{})
	d.Dial = func(context.Context, string) (net.Conn, error) { t.Fatal("dialled"); return nil, nil }
	w, err := New("herdr", d).(Watcher).Watch(context.Background(), targets)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := w.Next(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Next = %v", err)
	}
}

func TestTmuxIsNotAWatcher(t *testing.T) {
	if _, ok := New("tmux", deps(&fake{}, nil, &clock{})).(Watcher); ok {
		t.Error("tmux has no event stream; the caller polls it")
	}
}
