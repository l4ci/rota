package worker

import (
	"testing"
	"time"

	"github.com/l4ci/rota/internal/host"
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
			PollRow{"w1", StateDone, noPREvidence}},
		{"a review reply reads as done", &Event{Seq: 3, Kind: EventReviewReply, Body: "u", TS: at(0)}, idle, base, PollRow{"w1", StateDone, "u"}},
		{"an unknown native state is not a pulse either", blocked, PollRow{"w1", StateUnknown, "x"}, base, PollRow{"w1", StateBlocked, "A or B?"}},
		{"a pane that moved after the event wins", blocked, PollRow{"w1", StateBusy, "pane changed between captures"}, base,
			PollRow{"w1", StateBusy, "pane changed between captures"}},
		{"an old DONE sentinel in scrollback loses to a newer blocked event", blocked, PollRow{"w1", StateDone, "https://github.com/o/r/pull/7"}, base,
			PollRow{"w1", StateBlocked, "A or B?"}},
		{"an old BLOCKED sentinel loses to a newer blocked event", blocked, PollRow{"w1", StateBlocked, "old question"}, base,
			PollRow{"w1", StateBlocked, "A or B?"}},
		{"a DONE sentinel keeps its PR over a done event", done, PollRow{"w1", StateDone, "https://github.com/o/r/pull/8"}, base,
			PollRow{"w1", StateDone, "https://github.com/o/r/pull/8"}},
		{"a sentinel older than the last change is not weighed: the event is dead", blocked, PollRow{"w1", StateDone, "x"}, base.Add(time.Second),
			PollRow{"w1", StateDone, "x"}},
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

// AC-2 as the issue means it: an old ROTA-DONE in the pane, a relay, a rest,
// then a `--blocked` event. The wait must report the question, not the old PR.
func TestWaitReportsABlockedEventOverAnOldDoneSentinel(t *testing.T) {
	dir := waitProject(t, 1, map[string]string{"w1": "w9:t1"})
	if err := Update(dir, func(d *Doc) {
		s := d.Slot("w1")
		now := stamp(time.Now())
		s.SetEvent(EventBlocked, "A or B?", now)
		s.MarkState("blocked", now)
		s.ClearSeen()
	}); err != nil {
		t.Fatal(err)
	}
	h := newWaitHost("herdr")
	h.set("w1", "ROTA-DONE w1 https://github.com/o/r/pull/7\n--- ORCHESTRATOR (round 1) ---\nplease fix\n", "idle")
	res, err := envWith(watcherHost{h}).Wait(bg, dir, WaitOpts{})
	if err != nil || res.State != StateBlocked || res.Evidence != "A or B?" {
		t.Fatalf("%+v %v, want BLOCKED with the question", res, err)
	}
	if got := LoadRegistryTolerant(dir).Slot("w1"); got.PR() != "" || got.State() != "blocked" {
		t.Errorf("slot = %s pr %q: the old PR was recorded", got.State(), got.PR())
	}
}

// A `worker send` after Wait started, while the pane was still busy, must be
// read by that wait once the pane rests.
func TestWaitSeesAnEventSentWhileItWaits(t *testing.T) {
	dir := waitProject(t, 1, map[string]string{"w1": "w9:t1"})
	h := newWaitHost("herdr")
	h.set("w1", "working...\n", "working")
	captures := 0
	h.onCapture = func(string) {
		captures++
		if captures != 2 {
			return
		}
		if err := Update(dir, func(d *Doc) {
			s := d.Slot("w1")
			now := stamp(time.Now())
			s.SetEvent(EventBlocked, "A or B?", now)
			s.MarkState("blocked", now)
			s.ClearSeen()
		}); err != nil {
			t.Error(err)
		}
		h.mu.Lock()
		h.text["w1"], h.status["w1"] = "", "idle"
		h.mu.Unlock()
	}
	res, err := envWith(watcherHost{h}).Wait(bg, dir, WaitOpts{Timeout: 5 * time.Second})
	if err != nil || res.TimedOut || res.State != StateBlocked || res.Evidence != "A or B?" {
		t.Fatalf("%+v %v, want BLOCKED with the question", res, err)
	}
	if ev := LoadRegistryTolerant(dir).Slot("w1").Event(); ev == nil {
		t.Error("the event was killed by the pane read that raced it")
	}
}

// recordRow of a busy row read before an event arrived must not delete it.
func TestRecordRowKeepsAnEventNewerThanTheRead(t *testing.T) {
	s := NewSlot("w1", "b", "wt", "main", "h")
	s.MarkState("busy", "2026-10-16T12:00:00Z")
	read := slotTarget(s) // the snapshot the pane was read against: no event
	s.SetEvent(EventBlocked, "A or B?", "2026-10-16T12:00:05Z")
	skipped, err := recordRow(s, PollRow{"w1", StateBusy, "pane changed between captures"}, read, time.Now(), Registry{})
	if err != nil || !skipped {
		t.Fatalf("skipped=%v err=%v: a row read before the event must not be recorded", skipped, err)
	}
	if s.Event() == nil || s.State() != "busy" {
		t.Errorf("event %+v state %s", s.Event(), s.State())
	}
	// A busy row read against the standing event answers it.
	read = slotTarget(s)
	if skipped, err = recordRow(s, PollRow{"w1", StateBusy, "pane changed between captures"}, read, time.Now(), Registry{}); err != nil || skipped {
		t.Fatalf("skipped=%v err=%v", skipped, err)
	}
	if s.Event() != nil {
		t.Error("an answered event stands")
	}
}

// The live gap: `worker done` recorded the slot done before its PR existed, the
// pane is static with no sentinel, and the next wait re-recorded it as idle. The
// done event keeps it done, and the open-PR lookup by branch head still runs.
func TestWaitKeepsWorkerDoneOverAnIdlePane(t *testing.T) {
	w := newWorld(t, "")
	w.forge("listed", "1")
	if err := Update(w.dir, func(d *Doc) {
		s := d.Slot("w1")
		now := stamp(time.Now())
		s.SetHandle("w9:t1")
		s.SetEvent(EventDone, "", now)
		s.MarkState("done", now)
		s.ClearSeen()
	}); err != nil {
		t.Fatal(err)
	}
	h := newWaitHost("herdr")
	h.set("w1", "", "idle")
	e := w.env(false)
	e.NewHost = func(host.Kind) host.Host { return watcherHost{h} }
	res, err := e.Wait(bg, w.dir, WaitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if res.State != StateDone || res.Evidence != ghURL || res.Note != missingSentinelNote {
		t.Errorf("%+v, want DONE with the PR found by branch head", res)
	}
	if got := LoadRegistryTolerant(w.dir).Slot("w1"); got.State() != "done" || got.PR() != ghURL {
		t.Errorf("slot = %s pr %q", got.State(), got.PR())
	}
}
