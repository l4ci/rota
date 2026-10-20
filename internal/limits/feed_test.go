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

// errNoWatch is what fakeOW returns once its queue is spent: a subscription
// the test did not expect, which the pane-set count then reports.
var errNoWatch = errors.New("fakeOW: no queued watch")

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
	if len(o.queue) == 0 {
		return nil, errNoWatch
	}
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

// A stream that ends because Stop cancelled the feed is not a dead stream:
// the feed must neither open a new subscription nor degrade. The tick blocks
// in targets while the stream's error is read and the feed is cancelled, so
// the loop then finds both ready at once, the order a loaded machine
// produces. The short sleep only lets the reader hand the error over; the
// fixed feed passes however the two are ordered.
func TestFeedStopIsNotADeadStream(t *testing.T) {
	for i := 0; i < 200; i++ {
		w := newFakeWatch()
		ow := &fakeOW{queue: []any{w}}
		entered, release := make(chan struct{}, 1), make(chan struct{})
		var calls int
		targets := func(context.Context) []Target {
			if calls++; calls == 2 {
				entered <- struct{}{}
				<-release
			}
			return []Target{{Session: "ben", Pane: "p1"}}
		}
		f, err := NewFeed(context.Background(), ow, targets, "x", time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		degraded := make(chan error, 1)
		f.OnDegrade = func(err error) { degraded <- err }
		f.Start()
		<-entered
		w.err <- errors.New("closed")
		time.Sleep(time.Millisecond)
		f.cancel()
		close(release)
		f.Stop()
		if n := ow.calls(); n != 1 {
			t.Fatalf("run %d: subscriptions = %d after Stop, want 1", i, n)
		}
		select {
		case err := <-degraded:
			t.Fatalf("run %d: degraded on Stop: %v", i, err)
		default:
		}
	}
}
