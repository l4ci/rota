package worker

import (
	"context"
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
)

// WaitOpts are the flags of `rota round wait`.
type WaitOpts struct {
	Slots   []string      // empty: every slot with a handle whose recorded state is not idle
	Timeout time.Duration // 0 waits indefinitely
	Settle  time.Duration // gap between the two captures of one classification
	Lines   int
}

// Wait sources: what made the slot come back.
const (
	SourceSnapshot = "snapshot"
	SourceEvent    = "herdr-event"
	SourcePoll     = "poll"
	SourceRegistry = "registry" // solo: the recorded state, no pane to read
)

// WaitResult is the first slot that needs attention, or, with TimedOut, the
// state of every watched slot when the timeout passed.
type WaitResult struct {
	Slot, State, Evidence, Source string
	Waited                        time.Duration
	TimedOut                      bool
	Slots                         []PollRow
}

// Wait blocks until a watched slot needs attention. The host only wakes it:
// every wake re-classifies through the same code as `rota worker poll`, so
// sentinels, LIMITED and DEAD outrank the host's native status. It reads the
// registry and writes nothing.
//
// herdr (Watcher): subscribe, classify, then block on events. Subscribing
// FIRST means a change during the classification is already queued, so no
// event is lost. tmux has no event stream: it re-classifies in a loop, and
// each classification's settle gap is the poll interval.
//
// One exception on herdr (#211): a slot that is BUSY only because its pane
// moved while herdr did not say `working` is re-classified at once, without
// waiting. herdr sends one event per status change and none when the pane of
// an idle or done agent comes to rest, so blocking on the next event there
// would wait for an event that never comes. Right after a turn, two of
// herdr's scrollback reads can differ for a moment, which is how a finished
// worker reads as moving. The re-check is a poll and is reported as one.
func (e Env) Wait(ctx context.Context, root string, o WaitOpts) (WaitResult, error) {
	e = e.withDefaults()
	if o.Lines <= 0 {
		o.Lines = 60
	}
	start := e.Now()
	if RegistryHost(root) == host.Solo {
		return soloWait(root, o)
	}
	h := e.NewHost(hostKind(root))
	if err := h.Require(); err != nil {
		return WaitResult{}, fail(exitcode.ExitUnavailable, err.Error())
	}
	reg := LoadRegistry(root)
	var targets []pollTarget
	if len(o.Slots) > 0 {
		for _, name := range o.Slots {
			s := reg.Slot(name)
			if s == nil {
				return WaitResult{}, fail(exitcode.ExitResolution, fmt.Sprintf("slot '%s' is not in the pool", name))
			}
			t := slotTarget(s)
			if t.handle == "" {
				return WaitResult{}, fail(exitcode.ExitResolution, fmt.Sprintf("slot '%s' has no session to watch", name))
			}
			targets = append(targets, t)
		}
	} else {
		// Pool init seeds every slot idle, and gives a tmux slot a nominal
		// handle (`rota:w1`) before anything runs in it. A recorded `idle` is
		// "already reported or never started" (see Poll), so it is not
		// watched, or a parked slot would end every wait at once. Dispatch
		// records busy, which arms the slot.
		for _, s := range reg.Slots() {
			if t := slotTarget(s); t.handle != "" && strings.ToLower(t.prev) != "idle" {
				targets = append(targets, t)
			}
		}
	}
	if len(targets) == 0 {
		return WaitResult{}, fail(exitcode.ExitResolution, "no slot with a session to watch")
	}

	if o.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.Timeout)
		defer cancel()
	}

	// stop ends the wait when ctx did: the timeout is an answer, a signal is
	// not. A classification cut short by ctx read cancelled captures, so the
	// slot list reports the last complete one.
	var last []PollRow
	stop := func() (WaitResult, error) {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return WaitResult{TimedOut: true, Waited: e.Now().Sub(start), Slots: last}, nil
		}
		return WaitResult{}, fail(exitcode.ExitFailed, "interrupted")
	}
	var w host.Watch
	if wh, ok := h.(host.Watcher); ok {
		wt := make([]host.WatchTarget, len(targets))
		for i, t := range targets {
			wt[i] = host.WatchTarget{Slot: t.name, Handle: t.handle}
		}
		var err error
		if w, err = wh.Watch(ctx, wt); err != nil {
			if ctx.Err() != nil {
				return stop()
			}
			return WaitResult{}, fail(exitcode.ExitUnavailable, err.Error())
		}
		defer w.Close()
	}

	// seen is one snapshot of the registry; clears are kept in memory.
	seen := map[string]string{}
	for _, t := range targets {
		if t.seen != "" {
			seen[t.name] = t.seen
		}
	}
	source := SourceSnapshot
	for {
		rows, settling := e.classify(ctx, h, targets, o.Settle, o.Lines)
		if ctx.Err() != nil {
			return stop()
		}
		last = rows
		for _, r := range rows {
			key := seenKey(r.State, r.Evidence)
			if seen[r.Name] != "" && seen[r.Name] != key {
				// The slot moved on: re-arm it.
				delete(seen, r.Name)
				if _, err := updateSlot(root, r.Name, func(s *jsonx.Object) { s.Delete("seen") }); err != nil {
					return WaitResult{}, err
				}
			}
			// A row already returned once is treated like busy until the slot
			// shows a different state or evidence; alwaysNews states never are.
			if r.State != StateBusy && (seen[r.Name] != key || alwaysNews(r.State)) {
				if _, err := updateSlot(root, r.Name, func(s *jsonx.Object) {
					recordRow(s, r, e.Now())
					if !alwaysNews(r.State) {
						s.Set("seen", key)
					}
				}); err != nil {
					return WaitResult{}, err
				}
				return WaitResult{Slot: r.Name, State: r.State, Evidence: r.Evidence,
					Source: source, Waited: e.Now().Sub(start)}, nil
			}
		}
		if w == nil || settling {
			source = SourcePoll
			if o.Settle <= 0 {
				e.Sleep(time.Second)
			}
			continue
		}
		source = SourceEvent
		if _, err := w.Next(ctx); err != nil {
			if ctx.Err() != nil {
				return stop()
			}
			return WaitResult{}, fail(exitcode.ExitUnavailable, strings.TrimSpace(err.Error()))
		}
	}
}

// soloWait is Wait for a solo round. Nothing but `round report` changes a solo
// slot's state, so waiting would never end: it answers at once with the first
// watched slot whose recorded state is not busy and not already returned (`seen`), else timed out. Watched slots
// are the named ones, or every registered slot that is not idle; a solo slot
// has no handle to require.
func soloWait(root string, o WaitOpts) (WaitResult, error) {
	reg := LoadRegistry(root)
	var watched []*jsonx.Object
	if len(o.Slots) > 0 {
		for _, name := range o.Slots {
			s := reg.Slot(name)
			if s == nil {
				return WaitResult{}, fail(exitcode.ExitResolution, fmt.Sprintf("slot '%s' is not in the pool", name))
			}
			watched = append(watched, s)
		}
	} else {
		for _, s := range reg.Slots() {
			if strings.ToLower(Str(s, "state")) != "idle" {
				watched = append(watched, s)
			}
		}
	}
	if len(watched) == 0 {
		return WaitResult{}, fail(exitcode.ExitResolution, "no slot to watch: every slot is idle")
	}
	var rows []PollRow
	for _, s := range watched {
		st := strings.ToLower(Str(s, "state"))
		key := seenKey(st, "")
		if news := alwaysNews(strings.ToUpper(st)); st != "busy" && (news || key != Str(s, "seen")) {
			name := Str(s, "name")
			if _, err := updateSlot(root, name, func(s *jsonx.Object) {
				if !news {
					s.Set("seen", key)
				}
			}); err != nil {
				return WaitResult{}, err
			}
			return WaitResult{Slot: name, State: st, Source: SourceRegistry}, nil
		}
		rows = append(rows, PollRow{Str(s, "name"), st, ""})
	}
	return WaitResult{TimedOut: true, Slots: rows}, nil
}
