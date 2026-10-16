package worker

import (
	"testing"
	"time"
)

func TestDecide(t *testing.T) {
	base := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) string { return stamp(base.Add(d)) }
	idle := PollRow{"w1", StateIdle, "static pane, no sentinel"}
	blocked := &Event{Seq: 1, Kind: EventBlocked, Body: "A or B?", TS: at(0)}
	done := &Event{Seq: 2, Kind: EventDone, Body: "https://github.com/o/r/pull/7", TS: at(0)}
	for _, c := range []struct {
		name       string
		ev         *Event
		row        PollRow
		lastChange time.Time
		want       PollRow
	}{
		{"no event reads the pane", nil, idle, base, idle},
		{"a blocked event beats a static pane", blocked, idle, base, PollRow{"w1", StateBlocked, "A or B?"}},
		{"a question reads as blocked", &Event{Seq: 1, Kind: EventQuestion, Body: "Which?", TS: at(0)}, idle, base, PollRow{"w1", StateBlocked, "Which?"}},
		{"a done event beats a static pane", done, idle, base, PollRow{"w1", StateDone, done.Body}},
		{"a done event with no PR still says done", &Event{Seq: 2, Kind: EventDone, TS: at(0)}, idle, base,
			PollRow{"w1", StateDone, "worker done (recorded event)"}},
		{"a review reply reads as done", &Event{Seq: 3, Kind: EventReviewReply, Body: "u", TS: at(0)}, idle, base, PollRow{"w1", StateDone, "u"}},
		{"an unknown native state is not a pulse either", blocked, PollRow{"w1", StateUnknown, "x"}, base, PollRow{"w1", StateBlocked, "A or B?"}},
		{"a pane that moved after the event wins", blocked, PollRow{"w1", StateBusy, "pane changed between captures"}, base,
			PollRow{"w1", StateBusy, "pane changed between captures"}},
		{"a later sentinel wins", blocked, PollRow{"w1", StateDone, "x"}, base, PollRow{"w1", StateDone, "x"}},
		{"a dead session wins", done, PollRow{"w1", StateDead, "gone"}, base, PollRow{"w1", StateDead, "gone"}},
		{"a limit wins", blocked, PollRow{"w1", StateLimited, "l"}, base, PollRow{"w1", StateLimited, "l"}},
		{"a permission prompt wins", blocked, PollRow{"w1", StateNeedsPermission, "p"}, base, PollRow{"w1", StateNeedsPermission, "p"}},
		{"an event older than the last change (a dispatch stamps it too) is ignored", blocked, idle, base.Add(time.Second), idle},
		{"an event with no stamp is ignored", &Event{Seq: 1, Kind: EventBlocked, Body: "x"}, idle, base, idle},
		{"a heartbeat never decides", &Event{Seq: 4, Kind: EventHeartbeat, TS: at(0)}, idle, base, idle},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := Decide(c.ev, c.row, c.lastChange); got != c.want {
				t.Errorf("Decide = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestSlotEventSeqSurvivesClear(t *testing.T) {
	s := NewSlot("w1", "b", "wt", "main", "")
	s.SetEvent(EventBlocked, "q", "2026-10-16T12:00:00Z")
	s.ClearEvent()
	if s.Event() != nil {
		t.Fatal("event not cleared")
	}
	s.SetEvent(EventBlocked, "q", "2026-10-16T12:00:05Z")
	if ev := s.Event(); ev.Seq != 2 {
		t.Errorf("seq = %d: an acknowledged event must never share an id with the next", ev.Seq)
	}
}

func TestDispatchClearsEvent(t *testing.T) {
	dir := waitProject(t, 1, map[string]string{"w1": "w9:t1"})
	if err := Update(dir, func(d *Doc) {
		d.Slot("w1").SetEvent(EventBlocked, "old question", "2026-10-16T12:00:00Z")
	}); err != nil {
		t.Fatal(err)
	}
	if err := recordDispatch(dir, "w1", "w9:t1", "42", "", nil, 0, "2026-10-16T12:05:00Z"); err != nil {
		t.Fatal(err)
	}
	s := LoadRegistryTolerant(dir).Slot("w1")
	if s.Event() != nil {
		t.Errorf("a stale event from the previous issue survived the dispatch: %+v", s.Event())
	}
	// A relay (no task) is an answer too.
	Update(dir, func(d *Doc) { d.Slot("w1").SetEvent(EventBlocked, "q", "2026-10-16T12:06:00Z") })
	if err := recordDispatch(dir, "w1", "w9:t1", "", "", nil, 0, "2026-10-16T12:07:00Z"); err != nil {
		t.Fatal(err)
	}
	if LoadRegistryTolerant(dir).Slot("w1").Event() != nil {
		t.Error("a relay did not clear the event it answers")
	}
}

// The live gap: `worker done` recorded the slot done, the pane is static with
// no sentinel and no open PR, and the next wait re-recorded it as idle.
func TestWaitKeepsWorkerDoneOverAnIdlePane(t *testing.T) {
	dir := waitProject(t, 1, map[string]string{"w1": "w9:t1"})
	if err := Update(dir, func(d *Doc) {
		s := d.Slot("w1")
		now := stamp(time.Now())
		s.SetEvent(EventDone, "", now)
		s.MarkState("done", now)
		s.ClearSeen()
	}); err != nil {
		t.Fatal(err)
	}
	h := newWaitHost("herdr")
	h.set("w1", "", "idle")
	res, err := envWith(watcherHost{h}).Wait(bg, dir, WaitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if res.State != StateDone {
		t.Errorf("wait returned %s (%s), want DONE", res.State, res.Evidence)
	}
	if got := LoadRegistryTolerant(dir).Slot("w1").State(); got != "done" {
		t.Errorf("registry state = %s, want done: wait re-recorded a worker-done slot as idle", got)
	}
}

func TestWaitReturnsAnEventOnceUntilTheNext(t *testing.T) {
	dir := waitProject(t, 1, map[string]string{"w1": "w9:t1"})
	send := func(body string) {
		if err := Update(dir, func(d *Doc) {
			s := d.Slot("w1")
			now := stamp(time.Now())
			s.SetEvent(EventBlocked, body, now)
			s.MarkState("blocked", now)
			s.ClearSeen()
		}); err != nil {
			t.Fatal(err)
		}
	}
	h := newWaitHost("herdr")
	h.set("w1", "", "idle")
	e := envWith(watcherHost{h})

	send("A or B?")
	res, err := e.Wait(bg, dir, WaitOpts{})
	if err != nil || res.State != StateBlocked || res.Evidence != "A or B?" {
		t.Fatalf("first: %+v %v", res, err)
	}
	if res, err = e.Wait(bg, dir, WaitOpts{Timeout: 30 * time.Millisecond}); err != nil || !res.TimedOut {
		t.Fatalf("returned the same event twice: %+v %v", res, err)
	}
	// The same question again is a new event, so it is news again.
	send("A or B?")
	if res, err = e.Wait(bg, dir, WaitOpts{}); err != nil || res.State != StateBlocked {
		t.Fatalf("a new event of the same text: %+v %v", res, err)
	}
	if got := seenField(dir, "w1", "seen"); got != seenKey(StateBlocked, "A or B?")+"\t#2" {
		t.Errorf("seen = %q, want the key to carry the event seq", got)
	}
}

func TestWaitDropsAnEventAPaneMovedPast(t *testing.T) {
	dir := waitProject(t, 1, map[string]string{"w1": "w9:t1"})
	if err := Update(dir, func(d *Doc) {
		s := d.Slot("w1")
		now := stamp(time.Now())
		s.SetEvent(EventBlocked, "A or B?", now)
		s.MarkState("blocked", now)
	}); err != nil {
		t.Fatal(err)
	}
	h := newWaitHost("herdr")
	h.set("w1", "working...\n", "working") // answered in the pane: work resumed
	e := envWith(watcherHost{h})
	if res, err := e.Wait(bg, dir, WaitOpts{Timeout: 30 * time.Millisecond}); err != nil || !res.TimedOut {
		t.Fatalf("busy: %+v %v", res, err)
	}
	if LoadRegistryTolerant(dir).Slot("w1").Event() != nil {
		t.Error("the answered event still stands")
	}
	// The pane now rests with no sentinel: it is idle, not blocked again.
	h.set("w1", "", "idle")
	res, err := e.Wait(bg, dir, WaitOpts{})
	if err != nil || res.State != StateIdle {
		t.Errorf("after the answer: %+v %v, want IDLE", res, err)
	}
}
