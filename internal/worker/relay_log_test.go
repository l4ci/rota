package worker

import (
	"strings"
	"testing"
)

const relayQueuedPR = "https://github.com/o/r/pull/9"

func relayCited(t *testing.T, dir string) string {
	t.Helper()
	tgt, err := LoadRegistryTolerant(dir).GateTarget(relayQueuedPR)
	if err != nil {
		t.Fatal(err)
	}
	return CheckApprovals("## Approvals\n- ok per orchestrator relay round 1\n", tgt.Relays())
}

// A relay to a slot that holds an issue is logged where the gate reads it.
func TestDispatchRelayLoggedForHeldIssue(t *testing.T) {
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	e := envWith(tmuxFake())
	if _, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "task\n"), Task: "12"}); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateSlot(dir, "w1", func(s *Slot) { s.SetPR(relayQueuedPR) }); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "use 1\n"), Relay: true}); err != nil {
		t.Fatal(err)
	}
	if msg := relayCited(t, dir); msg != "" {
		t.Errorf("held slot: %s", msg)
	}
}

// A relay to a parked slot whose PR waits in review lands on the queued record
// the gate resolves the PR to, not on the parked slot.
func TestDispatchRelayLoggedForQueuedPR(t *testing.T) {
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	e := envWith(tmuxFake())
	if _, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "task\n"), Task: "12"}); err != nil {
		t.Fatal(err)
	}
	if err := Update(dir, func(d *Doc) {
		d.QueuePR(QueuedPR{Issue: "12", PR: relayQueuedPR, From: "w1", Branch: "w1/12-x", Base: "main", Round: 1})
		d.Slot("w1").Park(false)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "use 1\n"), Relay: true}); err != nil {
		t.Fatal(err)
	}
	if msg := relayCited(t, dir); msg != "" {
		t.Errorf("queued PR: %s", msg)
	}
	if n := len(LoadRegistryTolerant(dir).Slot("w1").Relays()); n != 0 {
		t.Errorf("parked slot kept %d relays the gate never reads", n)
	}
}

// A relay with no issue and no queued PR to attach to is refused instead of
// logging a record the gate never reads.
func TestDispatchRelayWithoutTargetFails(t *testing.T) {
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	e := envWith(tmuxFake())
	if _, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "task\n"), Task: "12"}); err != nil {
		t.Fatal(err)
	}
	if err := Update(dir, func(d *Doc) { d.Slot("w1").Park(false) }); err != nil {
		t.Fatal(err)
	}
	_, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "use 1\n"), Relay: true})
	if err == nil || !strings.Contains(err.Error(), "no issue or queued PR") {
		t.Fatalf("want explicit refusal, got %v", err)
	}
	if n := len(LoadRegistryTolerant(dir).Slot("w1").Relays()); n != 0 {
		t.Errorf("refused relay was logged (%d)", n)
	}
}
