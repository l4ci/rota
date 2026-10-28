package worker

import (
	"os"
	"path/filepath"
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

// bug-16: `worker gate <PR>` for a PR the slot never recorded resolved to an
// external target with no relay log, so a cited relay failed provenance. The
// relays of the slot holding the PR's issue are the log.
func TestGateByPRNumberUsesRelaysOfTheSlotThatNeverRecordedIt(t *testing.T) {
	w := newWorld(t, "")
	gitq(t, w.worker, "push", "-q", "origin", "w1:dana/5-x")
	w.forge("head", "dana/5-x")
	w.setPRBody("Closes #5\n\n## Approvals\n- use the cache: orchestrator relay round 2\n")
	relays := `[{"round":2,"ts":"2026-10-02T10:00:00Z","summary":"use the cache"}]`
	os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"),
		[]byte(`{"slots":[{"name":"w1","branch":"dana/5-x","task":"#5","relays":`+relays+`}]}`), 0o644)
	res, err := w.env(false).Gate(bg, w.dir, GateOpts{Slot: "7", Base: "main", CheckOnly: true})
	if err != nil || res.Verdict == GateProvenanceFail {
		t.Fatalf("verdict = %q (%v): %+v", res.Verdict, err, res)
	}
}
