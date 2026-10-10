package worker

import (
	"context"
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/ledger"
)

// WaitOpts are the flags of `rota round wait`.
type WaitOpts struct {
	Slots   []string      // empty: every slot with a handle whose recorded state is not idle
	Timeout time.Duration // 0 waits indefinitely
	Settle  time.Duration // gap between the two captures of one classification
	Lines   int
}

// maxResubscribe is how many times in a row the herdr event stream may be lost
// (or fail to re-open) before Wait gives up on events and polls instead. A
// delivered event resets the count.
const maxResubscribe = 3

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
	Note                          string // set when the state did not come from a sentinel
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
	h := e.NewHost(e.hostKind(root))
	if err := h.Require(); err != nil {
		return WaitResult{}, fail(exitcode.ExitUnavailable, err.Error())
	}
	reg, err := LoadRegistry(root)
	if err != nil {
		return WaitResult{}, err
	}
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
	var wh host.Watcher
	var wt []host.WatchTarget
	if cw, ok := h.(host.Watcher); ok {
		wh = cw
		wt = make([]host.WatchTarget, len(targets))
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
		defer func() {
			if w != nil {
				w.Close()
			}
		}()
	}
	// resubscribe replaces a lost stream. The loop's next classification runs
	// after it, so a change while disconnected is read from current state
	// (the seen dedupe keeps it from being reported twice). After
	// maxResubscribe losses in a row it drops to polling and says so once.
	lost := 0
	resubscribe := func(cause error) {
		w.Close()
		w = nil
		for lost++; lost <= maxResubscribe; lost++ {
			nw, err := wh.Watch(ctx, wt)
			if err == nil {
				w = nw
				return
			}
			cause = err
			if ctx.Err() != nil {
				return
			}
			e.Sleep(time.Duration(lost) * time.Second)
		}
		fmt.Fprintf(e.Stderr, "rota: herdr event stream lost (%s); polling instead\n", strings.TrimSpace(cause.Error()))
	}

	// seen is one snapshot of the registry; clears are kept in memory.
	seen := map[string]string{}
	for _, t := range targets {
		if t.seen != "" {
			seen[t.name] = t.seen
		}
	}
	source := SourceSnapshot
	// A done or idle slot whose PR is merged has nothing left for the
	// orchestrator but a park (`round reconcile --apply`), so it is not news.
	// Merged is final, so each slot is asked about once per wait.
	merged := map[string]bool{}
	settled := func(r PollRow) bool {
		if r.State != StateDone && r.State != StateIdle {
			return false
		}
		m, asked := merged[r.Name]
		if !asked {
			s := LoadRegistryTolerant(root).Slot(r.Name) // Wait read the registry strictly up front; the closure cannot fail
			m = s != nil && e.prMerged(ctx, root, s)
			merged[r.Name] = m
		}
		return m
	}
	for {
		rows, settling, slept := e.classify(ctx, h, targets, o.Settle, o.Lines)
		if ctx.Err() != nil {
			return stop()
		}
		rows, notes := e.promoteIdleWithPR(ctx, root, rows)
		last = rows
		for _, r := range rows {
			key := seenKey(r.State, r.Evidence)
			if seen[r.Name] != "" && seen[r.Name] != key {
				// The slot moved on: re-arm it.
				delete(seen, r.Name)
				if _, err := UpdateSlot(root, r.Name, func(s *Slot) { s.ClearSeen() }); err != nil {
					return WaitResult{}, err
				}
			}
			// A row already returned once is treated like busy until the slot
			// shows a different state or evidence; alwaysNews states never are.
			if r.State != StateBusy && (seen[r.Name] != key || alwaysNews(r.State)) && !settled(r) {
				var rowErr error
				var done *ledger.Entry
				if _, err := UpdateSlot(root, r.Name, func(s *Slot) {
					prev := s.State()
					// UpdateSlot reads the registry strictly; this is a read inside its closure.
					if rowErr = recordRow(s, r, e.Now(), LoadRegistryTolerant(root)); rowErr == nil && !alwaysNews(r.State) {
						s.SetSeen(key)
					}
					if d, ok := paneDone(prev, s, r); ok && rowErr == nil {
						done = &d
					}
				}); err != nil {
					return WaitResult{}, err
				}
				if done != nil {
					LedgerDone(ctx, e.Accounts, root, *done)
				}
				if rowErr != nil {
					return WaitResult{}, rowErr
				}
				syncChanged(ctx, h, root, []PollRow{r}, targets)
				return WaitResult{Slot: r.Name, State: r.State, Evidence: r.Evidence, Note: notes[r.Name],
					Source: source, Waited: e.Now().Sub(start)}, nil
			}
		}
		if w == nil || settling {
			source = SourcePoll
			// The classification's settle is the poll interval; one that
			// read only finished turns did not settle, so pause here.
			switch {
			case o.Settle <= 0:
				e.Sleep(time.Second)
			case !slept:
				e.Sleep(o.Settle)
			}
			continue
		}
		source = SourceEvent
		if _, err := w.Next(ctx); err != nil {
			if ctx.Err() != nil {
				return stop()
			}
			if errors.Is(err, host.ErrStreamLost) {
				resubscribe(err)
				if ctx.Err() != nil {
					return stop()
				}
				continue
			}
			return WaitResult{}, fail(exitcode.ExitUnavailable, strings.TrimSpace(err.Error()))
		}
		lost = 0
	}
}

// soloWait is Wait for a solo round. Nothing but `round report` changes a solo
// slot's state, so waiting would never end: it answers at once with the first
// watched slot whose recorded state is not busy and not already returned (`seen`), else timed out. Watched slots
// are the named ones, or every registered slot that is not idle; a solo slot
// has no handle to require.
func soloWait(root string, o WaitOpts) (WaitResult, error) {
	reg, err := LoadRegistry(root)
	if err != nil {
		return WaitResult{}, err
	}
	var watched []*Slot
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
			if !s.IsExternal() && strings.ToLower(s.State()) != "idle" {
				watched = append(watched, s)
			}
		}
	}
	if len(watched) == 0 {
		return WaitResult{}, fail(exitcode.ExitResolution, "no slot to watch: every slot is idle")
	}
	var rows []PollRow
	for _, s := range watched {
		st := strings.ToLower(s.State())
		key := seenKey(st, "")
		if news := alwaysNews(strings.ToUpper(st)); st != "busy" && (news || key != s.Seen()) {
			name := s.Name()
			if _, err := UpdateSlot(root, name, func(s *Slot) {
				if !news {
					s.SetSeen(key)
				}
			}); err != nil {
				return WaitResult{}, err
			}
			return WaitResult{Slot: name, State: st, Source: SourceRegistry}, nil
		}
		rows = append(rows, PollRow{s.Name(), st, ""})
	}
	return WaitResult{TimedOut: true, Slots: rows}, nil
}
