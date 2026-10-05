package limits

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/host"
)

type fakeWatch struct {
	ch     chan host.OutputMatch
	err    chan error
	closed chan struct{}
	once   sync.Once
}

func newFakeWatch() *fakeWatch {
	return &fakeWatch{ch: make(chan host.OutputMatch, 4), err: make(chan error, 1), closed: make(chan struct{})}
}

func (w *fakeWatch) Next(ctx context.Context) (host.OutputMatch, error) {
	select {
	case m := <-w.ch:
		return m, nil
	case err := <-w.err:
		return host.OutputMatch{}, err
	case <-ctx.Done():
		return host.OutputMatch{}, ctx.Err()
	}
}

func (w *fakeWatch) Close() { w.once.Do(func() { close(w.closed) }) }

// fakeOW hands out queued watches (or errors) and records the pane sets asked for.
type fakeOW struct {
	mu    sync.Mutex
	queue []any // *fakeWatch or error
	asked [][]string
}

func (o *fakeOW) WatchOutput(_ context.Context, panes []string, _ string) (host.OutputWatch, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.asked = append(o.asked, panes)
	next := o.queue[0]
	o.queue = o.queue[1:]
	if err, ok := next.(error); ok {
		return nil, err
	}
	return next.(*fakeWatch), nil
}

func (o *fakeOW) calls() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.asked)
}

func recv(t *testing.T, ch <-chan Match) Match {
	t.Helper()
	select {
	case m := <-ch:
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("no match")
		return Match{}
	}
}

func waitClosed(t *testing.T, c <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-c:
	case <-time.After(2 * time.Second):
		t.Fatalf("%s not closed", what)
	}
}

func TestFeedDeliversMatchForKnownPaneOnly(t *testing.T) {
	w := newFakeWatch()
	ow := &fakeOW{queue: []any{w}}
	targets := func(context.Context) []Target { return []Target{{Session: "ben", Pane: "p1"}} }
	f, err := NewFeed(context.Background(), ow, targets, "x", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	f.Start()
	w.ch <- host.OutputMatch{Pane: "other", Line: "l", Text: "t"}
	w.ch <- host.OutputMatch{Pane: "p1", Line: "l", Text: "t"}
	if m := recv(t, f.Matches()); m != (Match{Session: "ben", Line: "l", Text: "t"}) {
		t.Fatalf("got %+v", m)
	}
	f.Stop()
	waitClosed(t, w.closed, "watch")
}

func TestFeedResubscribesWhenPanesChange(t *testing.T) {
	w1, w2 := newFakeWatch(), newFakeWatch()
	ow := &fakeOW{queue: []any{w1, w2}}
	var mu sync.Mutex
	ts := []Target{{Session: "ben", Pane: "p1"}}
	targets := func(context.Context) []Target {
		mu.Lock()
		defer mu.Unlock()
		return append([]Target(nil), ts...)
	}
	f, err := NewFeed(context.Background(), ow, targets, "x", 5*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	f.Start()
	defer f.Stop()
	mu.Lock()
	ts = append(ts, Target{Session: "dana", Pane: "p2"})
	mu.Unlock()
	waitClosed(t, w1.closed, "old watch")
	w2.ch <- host.OutputMatch{Pane: "p2", Line: "l", Text: "t"}
	if m := recv(t, f.Matches()); m.Session != "dana" {
		t.Fatalf("got %+v", m)
	}
	if n := ow.calls(); n != 2 {
		t.Fatalf("subscriptions = %d, want 2", n)
	}
}

func TestFeedReopensDeadStreamOnce(t *testing.T) {
	w1, w2 := newFakeWatch(), newFakeWatch()
	ow := &fakeOW{queue: []any{w1, w2}}
	targets := func(context.Context) []Target { return []Target{{Session: "ben", Pane: "p1"}} }
	f, err := NewFeed(context.Background(), ow, targets, "x", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	f.OnDegrade = func(error) { t.Error("degraded though the reopen worked") }
	f.Start()
	defer f.Stop()
	w1.err <- errors.New("closed")
	waitClosed(t, w1.closed, "dead watch")
	w2.ch <- host.OutputMatch{Pane: "p1", Line: "l", Text: "t"}
	recv(t, f.Matches())
}

func TestFeedDegradesWhenReopenFails(t *testing.T) {
	w1 := newFakeWatch()
	boom := errors.New("refused")
	ow := &fakeOW{queue: []any{w1, boom}}
	targets := func(context.Context) []Target { return []Target{{Session: "ben", Pane: "p1"}} }
	f, err := NewFeed(context.Background(), ow, targets, "x", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan error, 1)
	f.OnDegrade = func(err error) { got <- err }
	f.Start()
	w1.err <- errors.New("closed")
	select {
	case err := <-got:
		if !errors.Is(err, boom) {
			t.Fatalf("degrade err = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no degrade")
	}
	f.Stop() // the goroutine already ended; Stop must not hang
}

func TestNewFeedFirstSubscribeError(t *testing.T) {
	boom := errors.New("unsupported")
	ow := &fakeOW{queue: []any{boom}}
	_, err := NewFeed(context.Background(), ow, func(context.Context) []Target { return nil }, "x", time.Hour)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}
