package round

import (
	"testing"

	"github.com/l4ci/rota/internal/worker"
)

// A relay sent about a PR stays checkable by the gate after the slot takes a
// new task: relay, reassign, gate the parked PR (#3266).
func TestParkedPRKeepsItsRelayAfterTheSlotIsReassigned(t *testing.T) {
	f, _, o := reviewFx(t)
	if _, err := f.env.ReviewRelay(bg, f.root, o); err != nil {
		t.Fatal(err)
	}
	// the worker answers the bounce and hands the PR back
	if _, err := worker.UpdateSlot(f.root, "ben", func(s *worker.Slot) { _ = s.MarkState("done", "") }); err != nil {
		t.Fatal(err)
	}
	if _, err := f.assign("13", "ben"); err != nil {
		t.Fatal(err)
	}
	tg, err := worker.LoadRegistryTolerant(f.root).GateTargetAny("9")
	if err != nil {
		t.Fatal(err)
	}
	approvals := "## Approvals\n- review bounce: orchestrator relay round 1\n"
	if msg := worker.CheckApprovals(approvals, tg.Relays()); msg != "" {
		t.Errorf("a relay that was sent must stay citable after the slot moved on: %s", msg)
	}
	if msg := worker.CheckApprovals("## Approvals\n- orchestrator relay round 9\n", tg.Relays()); msg == "" {
		t.Error("a relay round that was never sent must still fail")
	}
}
