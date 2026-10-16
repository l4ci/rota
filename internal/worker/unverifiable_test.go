package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/host"
)

// unreadableHost fails every pane read, like a herdr that answers `agent get`
// but cannot read the pane.
type unreadableHost struct{ *fakeHost }

func (unreadableHost) Capabilities() host.Capabilities { return host.CapabilitiesOf(unreadableHost{}) }
func (unreadableHost) ReadPane(context.Context, string, string, int) (string, error) {
	return "", errors.New("read failed")
}

func TestClassifyUnverifiableHostStatus(t *testing.T) {
	for native, reason := range map[string]string{
		"unverifiable:host-unreachable": "host-unreachable",
		"unverifiable:stale-handle":     "stale-handle",
		"unverifiable:pane-unreadable":  "pane-unreadable",
	} {
		if st, ev := Classify("  claude ready\n> \n", false, 60, native); st != "UNVERIFIABLE" || ev != reason {
			t.Errorf("%s: %s %q, want UNVERIFIABLE %q", native, st, ev, reason)
		}
	}
	// Positive proof outranks the absence of an answer: a printed sentinel is
	// still read, and a gone agent is still dead.
	if st, _ := Classify("ROTA-DONE w1 https://github.com/o/r/pull/9\n", false, 60, "unverifiable:host-unreachable"); st != StateDone {
		t.Errorf("sentinel under unverifiable: %s", st)
	}
	if st, _ := Classify("", false, 60, "gone"); st != StateDead {
		t.Errorf("gone: %s", st)
	}
}

func TestPollRecordsUnverifiableWithItsReason(t *testing.T) {
	dir, f := pollRegistry(t, "herdr")
	f.panes["w1"] = []string{"x\n", "x\n"}
	f.status["w1"] = "unverifiable:stale-handle"
	res, err := envWith(f).Poll(bg, dir, PollOpts{Lines: 60, Slot: "w1"})
	if err != nil || len(res.Slots) != 1 || res.Slots[0] != (PollRow{"w1", "UNVERIFIABLE", "stale-handle"}) {
		t.Fatalf("%+v %v", res, err)
	}
	if got := slotField(t, dir, "w1", "state"); got != "unverifiable" {
		t.Errorf("state = %s, want unverifiable", got)
	}
	// round status reads the reason from the slot, so it must outlive the poll.
	if got := seenField(dir, "w1", "seen"); got != seenKey("UNVERIFIABLE", "stale-handle") {
		t.Errorf("seen = %q", got)
	}
}

func TestPollReadsAFailedPaneReadAsUnverifiableNotIdle(t *testing.T) {
	dir, f := pollRegistry(t, "herdr")
	f.status["w1"] = "idle"
	res, err := envWith(unreadableHost{f}).Poll(bg, dir, PollOpts{Lines: 60, Slot: "w1"})
	if err != nil || len(res.Slots) != 1 || res.Slots[0] != (PollRow{"w1", "UNVERIFIABLE", "pane-unreadable"}) {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestWaitKeepsWaitingThroughAnUnverifiableSlot(t *testing.T) {
	dir := waitProject(t, 1, map[string]string{"w1": "w9:t1"})
	h := newWaitHost("herdr")
	h.set("w1", "", "unverifiable:host-unreachable")
	res, err := envWith(watcherHost{h}).Wait(bg, dir, WaitOpts{Timeout: 60 * time.Millisecond})
	if err != nil || !res.TimedOut {
		t.Fatalf("an unverifiable slot is not news: %+v %v", res, err)
	}
	// gone is, as ever.
	h.set("w1", "", "gone")
	if res, err = envWith(watcherHost{h}).Wait(bg, dir, WaitOpts{Timeout: time.Second}); err != nil || res.State != StateDead {
		t.Fatalf("gone: %+v %v", res, err)
	}
}

func TestSlotAcceptsTheUnverifiableState(t *testing.T) {
	if !ValidState("unverifiable") {
		t.Error("MarkState would refuse unverifiable")
	}
	if NeedsAttention("unverifiable") {
		t.Error("unverifiable is not news on its own")
	}
}

func TestADoneEventOutranksAnUnverifiablePane(t *testing.T) {
	ev := &Event{Kind: EventDone, Body: "https://github.com/o/r/pull/3", TS: "2026-10-16T12:05:00Z", Seq: 1}
	row, used := decide(ev, PollRow{"w1", "UNVERIFIABLE", "host-unreachable"}, parseStamp("2026-10-16T12:00:00Z"))
	if !used || row.State != StateDone {
		t.Errorf("the worker's own recorded word is evidence the host's silence is not: %+v", row)
	}
}

func TestWaitRecordsAnUnverifiableSlotAndItsRecovery(t *testing.T) {
	dir := waitProject(t, 1, map[string]string{"w1": "w9:t1"})
	h := newWaitHost("herdr")
	h.set("w1", "", "unverifiable:host-unreachable")
	e := envWith(watcherHost{h})
	if res, err := e.Wait(bg, dir, WaitOpts{Timeout: 60 * time.Millisecond}); err != nil || !res.TimedOut {
		t.Fatalf("%+v %v", res, err)
	}
	if got := seenField(dir, "w1", "state"); got != "unverifiable" {
		t.Errorf("state = %q: status and reclaim read the registry, not the wait", got)
	}
	if got := seenField(dir, "w1", "seen"); got != seenKey("UNVERIFIABLE", "host-unreachable") {
		t.Errorf("seen = %q", got)
	}
	h.set("w1", "working...\n", "working")
	if res, err := e.Wait(bg, dir, WaitOpts{Timeout: 60 * time.Millisecond}); err != nil || !res.TimedOut {
		t.Fatalf("%+v %v", res, err)
	}
	if got := seenField(dir, "w1", "state"); got != "busy" {
		t.Errorf("state = %q after the host answered again", got)
	}
}
