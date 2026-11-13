package worker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
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
	gittest.Run(t, w.worker, "push", "-q", "origin", "w1:dana/5-x")
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

// A PR on another branch that merely carries the slot's issue number in its
// name (a best-of sibling, another agent's branch) did not get the slot's
// relays: provenance matches on the exact branch only.
func TestGateByPRNumberDoesNotInheritRelaysOfAnotherBranchWithTheSameIssue(t *testing.T) {
	w := newWorld(t, "")
	gittest.Run(t, w.worker, "push", "-q", "origin", "w1:dana/5-x")
	w.forge("head", "dana/5-x")
	w.setPRBody("Closes #5\n\n## Approvals\n- use the cache: orchestrator relay round 2\n")
	relays := `[{"round":2,"ts":"2026-10-02T10:00:00Z","summary":"use the cache"}]`
	os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"),
		[]byte(`{"slots":[{"name":"w1","branch":"nia/5-y","task":"#5","relays":`+relays+`}]}`), 0o644)
	res, err := w.env(false).Gate(bg, w.dir, GateOpts{Slot: "7", Base: "main", CheckOnly: true})
	if err != nil || res.Verdict != GateProvenanceFail {
		t.Fatalf("verdict = %q (%v), want %q: %+v", res.Verdict, err, GateProvenanceFail, res)
	}
}

// #3266: a relay sent about a PR stays checkable after the slot takes a new
// task. The task dispatch resets the slot's relay log, so it first keeps the
// log where the gate reads it, by PR.
func TestRelaySurvivesTheSlotsNextTask(t *testing.T) {
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
	if _, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "next\n"), Task: "13"}); err != nil {
		t.Fatal(err)
	}
	tgt, err := LoadRegistryTolerant(dir).GateTargetAny(relayQueuedPR)
	if err != nil {
		t.Fatal(err)
	}
	if msg := CheckApprovals("## Approvals\n- ok per orchestrator relay round 1\n", tgt.Relays()); msg != "" {
		t.Errorf("a relay that was sent must stay citable after the next task: %s", msg)
	}
	if msg := CheckApprovals("## Approvals\n- ok per orchestrator relay round 7\n", tgt.Relays()); msg == "" {
		t.Error("a relay round that was never sent must still fail")
	}
	// The next task's own PR cannot cite the previous task's relays.
	const other = "https://github.com/o/r/pull/10"
	if _, err := UpdateSlot(dir, "w1", func(s *Slot) { s.SetPR(other) }); err != nil {
		t.Fatal(err)
	}
	next, err := LoadRegistryTolerant(dir).GateTarget(other)
	if err != nil {
		t.Fatal(err)
	}
	if msg := CheckApprovals("## Approvals\n- ok per orchestrator relay round 1\n", next.Relays()); msg == "" {
		t.Error("the next task's PR inherited the previous task's relays")
	}
}

// A second queue write after a bounce must retain the first queue's relay,
// even when the source slot has already taken another task (#3266).
func TestRequeuedPRKeepsItsRelayAfterReassignment(t *testing.T) {
	w := newWorld(t, ghURL)
	e := envWith(tmuxFake())
	if _, err := UpdateSlot(w.dir, "w1", func(s *Slot) {
		s.SetTask("5")
		s.SetHandle("rota:1")
		s.Raw().Set("base", "main")
		s.Raw().Set("worktree", w.worker)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Dispatch(bg, w.dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "use the cache\n"), Relay: true}); err != nil {
		t.Fatal(err)
	}
	rec := QueuedPR{Issue: "5", Branch: "w1", PR: ghURL, Base: "main", From: "w1"}
	if err := Update(w.dir, func(d *Doc) {
		rec.Relays = d.Slot("w1").Relays()
		d.QueuePR(rec)
		d.Slot("w1").Park(false)
	}); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, w.worker, "switch", "-q", "main")
	if _, err := e.Dispatch(bg, w.dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "next\n"), Task: "6"}); err != nil {
		t.Fatal(err)
	}
	if err := Update(w.dir, func(d *Doc) {
		rec.Relays = d.Slot("w1").Relays()
		d.QueuePR(rec)
	}); err != nil {
		t.Fatal(err)
	}
	if msg := CheckApprovals("## Approvals\n- orchestrator relay round 1\n",
		LoadRegistryTolerant(w.dir).QueuedPR(ghURL).Relays); msg != "" {
		t.Fatalf("replacement queue record dropped the saved relay: %s", msg)
	}
	w.setPRBody("Closes #5\n\n## Approvals\n- orchestrator relay round 1\n")
	res, err := w.env(false).Gate(bg, w.dir, GateOpts{Slot: "7", Base: "main", CheckOnly: true})
	if err != nil || res.Verdict != GateFresh {
		t.Fatalf("requeued PR lost its real relay: %+v (%v)", res, err)
	}
	w.setPRBody("Closes #5\n\n## Approvals\n- orchestrator relay round 99\n")
	res, err = w.env(false).Gate(bg, w.dir, GateOpts{Slot: "7", Base: "main", CheckOnly: true})
	if err != nil || res.Verdict != GateProvenanceFail {
		t.Fatalf("unsent relay round passed: %+v (%v)", res, err)
	}
}

func TestRelayWithoutRecordedPRSurvivesParkingAndReassignment(t *testing.T) {
	testRelaySurvivesParkingAndReassignment(t, "")
}

func TestRelayWithRecordedPRSurvivesParkingAndReassignment(t *testing.T) {
	testRelaySurvivesParkingAndReassignment(t, ghURL)
}

func testRelaySurvivesParkingAndReassignment(t *testing.T, pr string) {
	w := newWorld(t, pr)
	e := envWith(tmuxFake())
	if _, err := UpdateSlot(w.dir, "w1", func(s *Slot) {
		s.SetTask("5")
		s.SetHandle("rota:1")
		s.Raw().Set("base", "main")
		s.Raw().Set("worktree", w.worker)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Dispatch(bg, w.dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "use the cache\n"), Relay: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateSlot(w.dir, "w1", func(s *Slot) {
		s.Park(false)
		// Assign binds the next issue and branch before dispatch resets relays.
		s.Bind(Binding{Task: "6", ClaimID: "w1@2"})
		s.SetBranch("next/6-task")
	}); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, w.worker, "switch", "-q", "main")
	if _, err := e.Dispatch(bg, w.dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "next\n"), Task: "6"}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ head, issue, round, want string }{
		{"w1", "5", "1", GateFresh},
		{"w1", "5", "99", GateProvenanceFail},
		{"other/5-sibling", "5", "1", GateProvenanceFail},
		{"next/6-task", "6", "1", GateProvenanceFail},
	} {
		t.Run(tc.head+"/"+tc.round, func(t *testing.T) {
			if tc.head != "w1" {
				gittest.Run(t, w.worker, "push", "-q", "origin", "w1:"+tc.head)
			}
			w.forge("head", tc.head)
			w.setPRBody("Closes #" + tc.issue + "\n\n## Approvals\n- orchestrator relay round " + tc.round + "\n")
			ref := "7"
			if tc.head != "w1" {
				ref = "8" // a different PR cannot cite this task's relays
			}
			res, err := w.env(false).Gate(bg, w.dir, GateOpts{Slot: ref, Base: "main", CheckOnly: true})
			if err != nil || res.Verdict != tc.want {
				t.Fatalf("gate = %+v (%v), want %s", res, err, tc.want)
			}
		})
	}
}
