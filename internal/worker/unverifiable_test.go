package worker

import (
	"context"
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/stamp"
	"sync"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/host"
)

// unreadableHost fails every pane read with err, like a herdr that answers
// `agent get` but cannot read the pane.
type unreadableHost struct {
	*fakeHost
	err error
}

func (u unreadableHost) Capabilities() host.Capabilities { return host.CapabilitiesOf(u) }
func (u unreadableHost) ReadPane(context.Context, string, string, int) (string, error) {
	return "", u.err
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
	// A printed sentinel is still read.
	if st, _ := Classify("ROTA-DONE w1 https://github.com/o/r/pull/9\n", false, 60, "unverifiable:host-unreachable"); st != StateDone {
		t.Errorf("sentinel under unverifiable: %s", st)
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
	// The reason is the slot's own field: status and reclaim read it, and it
	// does not ride on `seen`, which is the wait's already-returned marker.
	if got := slotField(t, dir, "w1", "reason"); got != "stale-handle" {
		t.Errorf("reason = %s", got)
	}
	if got := slotField(t, dir, "w1", "seen"); got != "<null>" {
		t.Errorf("seen = %s, must stay empty for an unverifiable slot", got)
	}
}

func TestPollReadsAFailedPaneRead(t *testing.T) {
	for name, tc := range map[string]struct {
		native string
		err    error
		want   PollRow
	}{
		"idle agent, unreadable pane":      {"idle", errors.New("read failed"), PollRow{"w1", "UNVERIFIABLE", "pane-unreadable"}},
		"no status, unreadable pane":       {"", errors.New("read failed"), PollRow{"w1", "UNVERIFIABLE", "pane-unreadable"}},
		"a dialog the host reports":        {"blocked", errors.New("read failed"), PollRow{"w1", "NEEDS-PERMISSION", "herdr: agent blocked on a dialog — answer it in the slot's tab, or widen round.workerCommand"}},
		"a working agent the host reports": {"working", errors.New("read failed"), PollRow{"w1", "BUSY", "herdr: agent working"}},
		"a window the host says is gone":   {"", fmt.Errorf("window: %w", host.ErrPaneGone), PollRow{"w1", "DEAD", "herdr: no agent in the slot's tab — the session exited"}},
	} {
		t.Run(name, func(t *testing.T) {
			dir, f := pollRegistry(t, "herdr")
			f.status["w1"] = tc.native
			res, err := envWith(unreadableHost{f, tc.err}).Poll(bg, dir, PollOpts{Lines: 60, Slot: "w1"})
			if err != nil || len(res.Slots) != 1 || res.Slots[0] != tc.want {
				t.Fatalf("%+v %v, want %+v", res, err, tc.want)
			}
		})
	}
}

func TestWaitKeepsWaitingThroughAnUnverifiableSlotAndRecordsIt(t *testing.T) {
	dir := waitProject(t, 1, map[string]string{"w1": "w9:t1"})
	h := newWaitHost("herdr")
	h.set("w1", "", "unverifiable:host-unreachable")
	e := envWith(watcherHost{h})
	for i := 0; i < 2; i++ {
		res, err := e.Wait(bg, dir, WaitOpts{Timeout: 60 * time.Millisecond})
		if err != nil || !res.TimedOut {
			t.Fatalf("an unverifiable slot is not news: %+v %v", res, err)
		}
	}
	if got := seenField(dir, "w1", "state"); got != "unverifiable" {
		t.Errorf("state = %q: status and reclaim read the registry, not the wait", got)
	}
	if got := seenField(dir, "w1", "reason"); got != "host-unreachable" {
		t.Errorf("reason = %q", got)
	}
	if got := seenField(dir, "w1", "seen"); got != "" {
		t.Errorf("seen = %q: the reason must not be rewritten into the returned marker", got)
	}
	h.set("w1", "working...\n", "working")
	if res, err := e.Wait(bg, dir, WaitOpts{Timeout: 60 * time.Millisecond}); err != nil || !res.TimedOut {
		t.Fatalf("%+v %v", res, err)
	}
	if got := seenField(dir, "w1", "state"); got != "busy" {
		t.Errorf("state = %q after the host answered again", got)
	}
	if got := seenField(dir, "w1", "reason"); got != "" {
		t.Errorf("reason = %q survived the recovery", got)
	}
}

// Persistent contact loss must reach the orchestrator once (like a stalled
// report); the first minutes stay quiet.
func TestWaitReturnsALongUnverifiableSlotOnce(t *testing.T) {
	dir := waitProject(t, 1, map[string]string{"w1": "w9:t1"})
	h := newWaitHost("herdr")
	h.set("w1", "", "unverifiable:host-unreachable")
	e := envWith(watcherHost{h})
	after := 30 * time.Minute
	if res, err := e.Wait(bg, dir, WaitOpts{Timeout: 60 * time.Millisecond, UnverifiableAfter: after}); err != nil || !res.TimedOut {
		t.Fatalf("the first minutes are quiet: %+v %v", res, err)
	}
	if err := Update(dir, func(d *Doc) { // contact has been lost for 40 minutes
		d.Slot("w1").Raw().Set("activeAt", stamp.Format(e.Now().Add(-40*time.Minute)))
	}); err != nil {
		t.Fatal(err)
	}
	res, err := e.Wait(bg, dir, WaitOpts{Timeout: time.Second, UnverifiableAfter: after})
	if err != nil || res.State != StateUnverifiable || res.Evidence != "host-unreachable" {
		t.Fatalf("past the threshold: %+v %v", res, err)
	}
	if res, err = e.Wait(bg, dir, WaitOpts{Timeout: 60 * time.Millisecond, UnverifiableAfter: after}); err != nil || !res.TimedOut {
		t.Fatalf("returned twice: %+v %v", res, err)
	}
}

// A herdr slot that is unverifiable emits no status event, so an indefinite
// wait blocked on events must still come due when the threshold passes.
func TestWaitOnEventsReturnsAnUnverifiableSlotWhenItComesDue(t *testing.T) {
	dir := waitProject(t, 1, map[string]string{"w1": "w9:t1"})
	h := newWaitHost("herdr") // never emits
	h.set("w1", "", "unverifiable:stale-handle")
	e := envWith(watcherHost{h})
	// A clock that starts at its first read, which is the wait's own: a
	// scheduler stall between the registry write and the wait cannot make the
	// slot overdue before the event wait begins.
	base := time.Date(2026, 10, 2, 15, 4, 5, 0, time.UTC)
	var first sync.Once
	var began time.Time
	e.Now = func() time.Time {
		first.Do(func() { began = time.Now() })
		return base.Add(time.Since(began))
	}
	if err := Update(dir, func(d *Doc) {
		s := d.Slot("w1")
		s.Raw().Set("state", "unverifiable")
		s.Raw().Set("reason", "stale-handle")
		s.Raw().Set("activeAt", stamp.Format(base.Add(-1*time.Second))) // due in about a second
	}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	res, err := e.Wait(bg, dir, WaitOpts{Timeout: 20 * time.Second, UnverifiableAfter: 2 * time.Second})
	if err != nil || res.State != StateUnverifiable || res.Evidence != "stale-handle" || res.Source != SourceEvent && res.Source != SourcePoll {
		t.Fatalf("%+v %v", res, err)
	}
	if took := time.Since(start); took > 15*time.Second {
		t.Errorf("returned after %s: the event wait was not bounded by the deadline", took)
	}
}
