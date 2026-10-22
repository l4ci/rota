package worker

import (
	"testing"

	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/host"
)

// A timed-out send may still reach the session. A reply after the relay echo
// acknowledges it, and later reply bullets must continue updating the PR.
func TestPollReplyAcknowledgesUnsentRelay(t *testing.T) {
	dir := slotProject(t)
	f := herdrFake()
	e := envWith(f)
	if _, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", Task: "T1", BodyFile: writeBrief(t, "start")}); err != nil {
		t.Fatal(err)
	}
	f.sendErr = host.ErrNotSubmitted
	if _, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", Relay: true, BodyFile: writeBrief(t, "continue")}); exitOf(err) != exitcode.ExitRetry {
		t.Fatalf("stalled relay: %v", err)
	}
	first := "--- ORCHESTRATOR (round 1) ---\n[relay 1]\n\n● ROTA-DONE w1 https://github.com/o/r/pull/9\n"
	next := "● ROTA-DONE w1 https://github.com/o/r/pull/10\n"
	f.status = map[string]string{"w1": "idle"}
	f.panes = map[string][]string{"w1": {first, first, next, next}}
	for _, want := range []string{"https://github.com/o/r/pull/9", "https://github.com/o/r/pull/10"} {
		res, err := e.Poll(bg, dir, PollOpts{Slot: "w1"})
		if err != nil || len(res.Slots) != 1 || res.Slots[0].State != StateDone || res.Slots[0].Evidence != want {
			t.Fatalf("reply should record %s: %+v, %v", want, res, err)
		}
		if got := LoadRegistryTolerant(dir).Slot("w1").PR(); got != want {
			t.Fatalf("recorded PR = %s, want %s", got, want)
		}
	}
	if LoadRegistryTolerant(dir).Slot("w1").Unsent() {
		t.Fatal("acknowledged relay must no longer be unsent")
	}
}
