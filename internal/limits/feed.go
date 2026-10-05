package limits

import (
	"context"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/host"
)

// Feed turns herdr's pane.output_matched events into Matches. It subscribes
// to the panes known at the start and again whenever the set of panes changes
// (a slot dispatched later), and reports a dead stream through OnDegrade.
type Feed struct {
	// OnDegrade is called once when the stream died and could not be opened
	// again; the feed is finished after that. Set it before Start.
	OnDegrade func(error)

	ow       host.OutputWatcher
	targets  func(ctx context.Context) []Target
	regex    string
	tick     time.Duration
	ctx      context.Context
	cancel   context.CancelFunc
	out      chan Match
	cur      host.OutputWatch
	key      string
	sessions map[string]string // pane -> session
	done     chan struct{}
}

// NewFeed opens the first subscription. Call Start to run it.
func NewFeed(ctx context.Context, ow host.OutputWatcher, targets func(context.Context) []Target, regex string, tick time.Duration) (*Feed, error) {
	fctx, cancel := context.WithCancel(ctx)
	f := &Feed{ow: ow, targets: targets, regex: regex, tick: tick, ctx: fctx, cancel: cancel,
		out: make(chan Match, 64), done: make(chan struct{})}
	if err := f.subscribe(); err != nil {
		cancel()
		return nil, err
	}
	return f, nil
}

// Matches is the channel the matches arrive on.
func (f *Feed) Matches() <-chan Match { return f.out }

// Stop ends the feed and waits for it to finish. Call it only after Start.
func (f *Feed) Stop() {
	f.cancel()
	<-f.done
}

// subscribe opens a subscription for the current panes, replacing the old one.
func (f *Feed) subscribe() error {
	ts := f.targets(f.ctx)
	panes := make([]string, 0, len(ts))
	sessions := map[string]string{}
	for _, t := range ts {
		panes = append(panes, t.Pane)
		sessions[t.Pane] = t.Session
	}
	key := strings.Join(panes, ",")
	w, err := f.ow.WatchOutput(f.ctx, panes, f.regex)
	if err != nil {
		return err
	}
	if f.cur != nil {
		f.cur.Close()
	}
	f.cur, f.key, f.sessions = w, key, sessions
	return nil
}

type feedMsg struct {
	m   host.OutputMatch
	err error
}

// Start runs the feed on its own goroutine.
func (f *Feed) Start() {
	go func() {
		defer close(f.done)
		defer func() {
			if f.cur != nil {
				f.cur.Close()
			}
		}()
		tick := time.NewTicker(f.tick)
		defer tick.Stop()
		var msgs chan feedMsg
		read := func(w host.OutputWatch) chan feedMsg {
			ch := make(chan feedMsg, 1)
			go func() {
				for {
					m, err := w.Next(f.ctx)
					select {
					case ch <- feedMsg{m, err}:
					case <-f.ctx.Done():
						return
					}
					if err != nil {
						return
					}
				}
			}()
			return ch
		}
		msgs = read(f.cur)
		for {
			select {
			case <-f.ctx.Done():
				return
			case r := <-msgs:
				if r.err != nil {
					// The stream is dead: try once to open it again.
					msgs = nil
					if err := f.subscribe(); err != nil {
						if f.OnDegrade != nil {
							f.OnDegrade(err)
						}
						return
					}
					msgs = read(f.cur)
					continue
				}
				if s := f.sessions[r.m.Pane]; s != "" {
					select {
					case f.out <- Match{Session: s, Line: r.m.Line, Text: r.m.Text}:
					case <-f.ctx.Done():
						return
					}
				}
			case <-tick.C:
				// a slot dispatched since the subscription has no events yet
				ts := f.targets(f.ctx)
				panes := make([]string, 0, len(ts))
				for _, t := range ts {
					panes = append(panes, t.Pane)
				}
				if strings.Join(panes, ",") != f.key {
					if err := f.subscribe(); err == nil {
						msgs = read(f.cur)
					}
				}
			}
		}
	}()
}
