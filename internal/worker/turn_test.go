package worker

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/host"
)

// turnHost is a fakeHost that numbers its agents' state changes, the way
// herdr's `agent get` does. A slot missing from turns is a failing read.
type turnHost struct {
	*fakeHost
	turns map[string]host.Turn
}

func (h turnHost) Turn(_ context.Context, slot, _ string) (host.Turn, bool) {
	h.calls = append(h.calls, "turn "+slot)
	t, ok := h.turns[slot]
	return t, ok
}

func countCalls(calls []string, call string) int {
	n := 0
	for _, c := range calls {
		if c == call {
			n++
		}
	}
	return n
}

func TestDispatchRecordsTheTurnBaselineBeforeSending(t *testing.T) {
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	f := tmuxFake()
	h := turnHost{f, map[string]host.Turn{"w1": {Status: "idle", StateSeq: 40}}}
	e := envWith(h)
	if _, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "task\n"), Task: "T1"}); err != nil {
		t.Fatal(err)
	}
	if got := LoadRegistry(dir).Slot("w1").TurnSeq(); got != 40 {
		t.Errorf("task dispatch: turnSeq = %d, want 40", got)
	}
	turn, send := -1, -1
	for i, c := range f.calls {
		switch {
		case c == "turn w1" && turn < 0:
			turn = i
		case strings.HasPrefix(c, "send w1"):
			send = i
		}
	}
	if turn < 0 || send < 0 || turn > send {
		t.Errorf("the baseline must be read before the brief goes out: %v", f.calls)
	}

	// A relay re-reads it: the previous turn's completion is behind it.
	h.turns["w1"] = host.Turn{Status: "idle", StateSeq: 57, CompletionSeq: 57}
	if _, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "answer\n"), Relay: true}); err != nil {
		t.Fatal(err)
	}
	if got := LoadRegistry(dir).Slot("w1").TurnSeq(); got != 57 {
		t.Errorf("relay: turnSeq = %d, want 57", got)
	}

	// A failing read clears it, so a stale baseline never vouches for a turn.
	delete(h.turns, "w1")
	if _, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "again\n"), Relay: true}); err != nil {
		t.Fatal(err)
	}
	if got := slotField(t, dir, "w1", "turnSeq"); got != "<null>" {
		t.Errorf("failing read kept turnSeq = %s", got)
	}
}

func TestDispatchOnAHostWithoutTurnsRecordsNoBaseline(t *testing.T) {
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	if _, err := envWith(tmuxFake()).Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "task\n"), Task: "T1"}); err != nil {
		t.Fatal(err)
	}
	if got := slotField(t, dir, "w1", "turnSeq"); got != "<null>" {
		t.Errorf("turnSeq = %s", got)
	}
}

// turnPoll polls slot w1 with base recorded and turn reported, the pane
// reading panes in order, and returns the row, the host calls and how many
// times the poll slept.
func turnPoll(t *testing.T, base int, turn host.Turn, panes ...string) (PollRow, []string, int) {
	t.Helper()
	dir, f := pollRegistry(t, "herdr")
	if _, err := UpdateSlot(dir, "w1", func(s *Slot) { s.SetHandle("w9:t1"); s.SetTurnSeq(base) }); err != nil {
		t.Fatal(err)
	}
	f.status["w1"] = turn.Status
	f.panes["w1"] = panes
	h := turnHost{f, map[string]host.Turn{"w1": turn}}
	e := envWith(h)
	slept := 0
	e.Sleep = func(time.Duration) { slept++ }
	res, err := e.Poll(bg, dir, PollOpts{Slot: "w1", Settle: time.Second, Lines: 60})
	if err != nil || len(res.Slots) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	return res.Slots[0], f.calls, slept
}

func TestPollFinishedTurnSkipsTheSecondCapture(t *testing.T) {
	// The pane would read as moving if captured twice; one capture after a
	// finished turn reads it as it rests.
	row, calls, slept := turnPoll(t, 40, host.Turn{Status: "done", StateSeq: 52, CompletionSeq: 52},
		"all done\n", "all done, redrawn\n")
	if row.State != StateIdle {
		t.Errorf("row = %+v", row)
	}
	if n := countCalls(calls, "capture w1"); n != 1 {
		t.Errorf("captures = %d, want 1: %v", n, calls)
	}
	if slept != 0 {
		t.Errorf("slept %d times; a finished turn needs no settle", slept)
	}
	// herdr's idle (a finished turn the person has seen) counts the same.
	if row, calls, _ := turnPoll(t, 40, host.Turn{Status: "idle", StateSeq: 60, CompletionSeq: 52}, "x\n", "y\n"); row.State != StateIdle || countCalls(calls, "capture w1") != 1 {
		t.Errorf("idle: %+v %v", row, calls)
	}
}

func TestPollFinishedTurnKeepsTheTextRules(t *testing.T) {
	finished := host.Turn{Status: "done", StateSeq: 52, CompletionSeq: 52}
	for name, c := range map[string]struct{ pane, state string }{
		"sentinel":   {"ROTA-BLOCKED w1: A or B?\n", StateBlocked},
		"done":       {"ROTA-DONE w1 https://github.com/o/r/pull/9\n", StateDone},
		"limited":    {"You've hit your usage limit. resets at 5pm\n", StateLimited},
		"retry":      {"API Error: Overloaded\nRetrying in 4 seconds\n", StateBusy},
		"permission": {"Do you want to proceed?\n 1. Yes\n", StateNeedsPermission},
		"dead":       {"API Error: 529 Overloaded\n", StateDead},
	} {
		t.Run(name, func(t *testing.T) {
			row, calls, _ := turnPoll(t, 40, finished, c.pane, "redrawn\n")
			if row.State != c.state || countCalls(calls, "capture w1") != 1 {
				t.Errorf("want %s on one capture, got %+v %v", c.state, row, calls)
			}
		})
	}
}

func TestPollWithoutAnAdvancedTurnKeepsTheMovementCheck(t *testing.T) {
	for name, c := range map[string]struct {
		base int
		turn host.Turn
	}{
		"seq unchanged":    {52, host.Turn{Status: "done", StateSeq: 52, CompletionSeq: 52}},
		"seq behind":       {60, host.Turn{Status: "idle", StateSeq: 61, CompletionSeq: 52}},
		"missing field":    {40, host.Turn{Status: "idle", StateSeq: 52}},
		"no baseline":      {0, host.Turn{Status: "done", StateSeq: 52, CompletionSeq: 52}},
		"working":          {40, host.Turn{Status: "working", StateSeq: 52}},
		"unknown agent":    {40, host.Turn{Status: "unknown", StateSeq: 52, CompletionSeq: 52}},
		"blocked on modal": {40, host.Turn{Status: "blocked", StateSeq: 52, CompletionSeq: 52}},
	} {
		t.Run(name, func(t *testing.T) {
			row, calls, slept := turnPoll(t, c.base, c.turn, "a\n", "b\n")
			if row.State != StateBusy || row.Evidence != "pane changed between captures" && c.turn.Status != "working" {
				t.Errorf("row = %+v", row)
			}
			if countCalls(calls, "capture w1") != 2 || slept != 1 {
				t.Errorf("want today's two captures and one settle, got %v (slept %d)", calls, slept)
			}
		})
	}
}

// turnWatcher is a watcherHost whose agents carry turn numbers: the status
// is the waitHost's, the completion number is set by the test.
type turnWatcher struct {
	watcherHost
	mu   *sync.Mutex
	done map[string]int
}

func (h turnWatcher) Turn(_ context.Context, slot, _ string) (host.Turn, bool) {
	h.waitHost.mu.Lock()
	st := h.waitHost.status[slot]
	h.waitHost.mu.Unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	return host.Turn{Status: st, StateSeq: 100, CompletionSeq: h.done[slot]}, true
}

// The #211 shape: right after the done event, two captures of a finished
// worker differ. With the turn numbered past the baseline, the event's own
// classification reads it as finished; nothing waits for a re-check.
func TestWaitFinishedTurnIsReadAtTheEventWithoutARecheck(t *testing.T) {
	dir := waitProject(t, 1, map[string]string{"w1": "w9:t1"})
	if _, err := UpdateSlot(dir, "w1", func(s *Slot) { s.SetTurnSeq(40) }); err != nil {
		t.Fatal(err)
	}
	h := newWaitHost("herdr")
	h.set("w1", "✻ Working…\n", "working")
	tw := turnWatcher{watcherHost{h}, &sync.Mutex{}, map[string]int{}}
	captures, armed := 0, false
	h.onCapture = func(string) {
		h.mu.Lock()
		defer h.mu.Unlock()
		if armed {
			captures++
			h.text["w1"] = strings.Repeat("redraw ", captures) + "\n" // every read differs
		} else {
			h.text["w1"] += "."
		}
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		tw.mu.Lock()
		tw.done["w1"] = 55
		tw.mu.Unlock()
		h.mu.Lock()
		h.status["w1"], armed = "done", true
		h.mu.Unlock()
		h.events <- "w1"
	}()
	res, err := envWith(tw).Wait(bg, dir, WaitOpts{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if res.TimedOut || res.State != StateIdle || res.Source != SourceEvent {
		t.Fatalf("want idle via the event, got %+v", res)
	}
	if captures != 1 {
		t.Errorf("captures after the event = %d, want 1", captures)
	}
}

// Polling (no event stream) a slot whose finished turn was already returned:
// its classification does not settle, so the loop pauses for --settle itself
// instead of spinning on the host.
func TestWaitPollingFinishedTurnsStillPausesBetweenReads(t *testing.T) {
	dir := waitProject(t, 1, map[string]string{"w1": "w9:t1"})
	if _, err := UpdateSlot(dir, "w1", func(s *Slot) { s.SetTurnSeq(40); s.SetSeen(seenKey(StateIdle, "static pane, no sentinel")) }); err != nil {
		t.Fatal(err)
	}
	h := newWaitHost("herdr")
	h.set("w1", "", "done")
	ctx, cancel := context.WithCancel(bg)
	var sleeps []time.Duration
	e := envWith(turnPollHost{h, 55})
	e.Sleep = func(d time.Duration) {
		sleeps = append(sleeps, d)
		if len(sleeps) == 3 {
			cancel()
		}
	}
	e.Wait(ctx, dir, WaitOpts{Settle: 2 * time.Second})
	for _, d := range sleeps {
		if d != 2*time.Second {
			t.Errorf("sleeps = %v, want each --settle", sleeps)
		}
	}
	if len(sleeps) < 3 {
		t.Errorf("sleeps = %v", sleeps)
	}
}

// turnPollHost is a waitHost (no Watch) whose agents finished turn done.
type turnPollHost struct {
	*waitHost
	done int
}

func (h turnPollHost) Turn(_ context.Context, slot, _ string) (host.Turn, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return host.Turn{Status: h.status[slot], StateSeq: h.done, CompletionSeq: h.done}, true
}
