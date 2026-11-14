package worker

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/stamp"
)

// submittedSlot dispatches a task whose Core delivery ends "submitted" and
// returns the project, the env and the rig's host. The host then proves nothing
// further, so only consumption evidence can free the slot.
func submittedSlot(t *testing.T) (string, Env, *fakeHost) {
	t.Helper()
	dir := newProject(t, `{"round":{"dispatch":"herdr-core"}}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	h := &fakeHost{receipts: true, name: host.HerdrCore, sendErr: host.ErrSubmitted}
	rig := &codexRig{loggedIn: true, missing: map[string]bool{"herdr": true}}
	e := rig.env(h)
	task := DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "work once"), Task: "T1", Kind: "codex"}
	if res, err := e.Dispatch(bg, dir, task); err != nil || res.Delivery != "submitted" {
		t.Fatalf("task: %+v %v, want submitted", res, err)
	}
	return dir, e, h
}

func relayOpts(t *testing.T) DispatchOpts {
	return DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "answer once"), Relay: true}
}

func hasCoreDelivery(dir string) bool {
	return corePending(LoadRegistryTolerant(dir).Slot("w1")) != nil
}

// #3320 AC-1: a done event after the delivery is consumption evidence.
func TestCoreDeliveryClearedByLaterDoneEvent(t *testing.T) {
	dir, e, h := submittedSlot(t)
	later := stamp.Format(e.Now().Add(time.Hour))
	if _, err := UpdateSlot(dir, "w1", func(s *Slot) { s.SetEvent(EventDone, "", later) }); err != nil {
		t.Fatal(err)
	}
	// The host still proves nothing: only the evidence can free the slot.
	if _, err := e.Dispatch(bg, dir, relayOpts(t)); err != nil {
		t.Logf("relay err: %v", err)
	}
	if !strings.Contains(h.sent, "answer once") {
		t.Errorf("relay not delivered; last send %q", h.sent)
	}
}

// A done event from before the delivery says nothing about it.
func TestCoreDeliveryKeptByEarlierDoneEvent(t *testing.T) {
	dir, e, _ := submittedSlot(t)
	earlier := stamp.Format(e.Now().Add(-time.Hour))
	if _, err := UpdateSlot(dir, "w1", func(s *Slot) { s.SetEvent(EventDone, "", earlier) }); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Dispatch(bg, dir, relayOpts(t)); err == nil || !strings.Contains(err.Error(), "unresolved delivery (state: submitted)") {
		t.Fatalf("an earlier done event must not clear the delivery: %v", err)
	}
}

// #3320 AC-1: a commit on the slot's branch after the delivery is evidence too.
func TestCoreDeliveryClearedByLaterBranchCommit(t *testing.T) {
	dir, e, h := submittedSlot(t)
	if _, err := UpdateSlot(dir, "w1", func(s *Slot) { s.SetBranch("w1/t1-x"); s.o.Set("base", "main") }); err != nil {
		t.Fatal(err)
	}
	commit := e.Now().Add(time.Hour)
	e.Git = func(ctx context.Context, d string, args ...string) (git.Result, error) {
		if len(args) > 0 && args[0] == "log" {
			return git.Result{Stdout: commit.Format(time.RFC3339) + "\n"}, nil
		}
		return git.Exec(ctx, d, args...)
	}
	if _, err := e.Dispatch(bg, dir, relayOpts(t)); err != nil {
		t.Logf("relay err: %v", err)
	}
	if !strings.Contains(h.sent, "answer once") {
		t.Errorf("relay not delivered; last send %q", h.sent)
	}
}

// #3320 AC-4: no evidence keeps today's refusal and hint.
func TestCoreDeliveryWithoutEvidenceStillRefuses(t *testing.T) {
	dir, e, _ := submittedSlot(t)
	_, err := e.Dispatch(bg, dir, relayOpts(t))
	if err == nil || !strings.Contains(err.Error(), "unresolved delivery (state: submitted)") || !strings.Contains(err.Error(), "rota round transfer") {
		t.Fatalf("no evidence must keep the refusal and hint: %v", err)
	}
	if !hasCoreDelivery(dir) {
		t.Fatal("refusal must keep the saved delivery")
	}
}

// #3320 AC-2: recording done clears the slot's saved delivery.
func TestWorkerDoneClearsCoreDelivery(t *testing.T) {
	dir, e, _ := submittedSlot(t)
	if !hasCoreDelivery(dir) {
		t.Fatal("setup: no saved delivery")
	}
	req := DoneRequest{Root: dir, PoolRoot: dir, Slot: "w1"}
	sl := LoadRegistryTolerant(dir).Slot("w1")
	res := &DoneResult{}
	if err := markDone(e, req, DoneDeps{Env: e}, sl, "T1", res); err != nil {
		t.Fatalf("markDone: %v", err)
	}
	if hasCoreDelivery(dir) {
		t.Fatal("worker done left the Core delivery behind")
	}
}

// #3320 AC-3: a task dispatch starts a fresh session; no earlier delivery blocks it.
func TestCoreFreshSessionIgnoresEarlierDelivery(t *testing.T) {
	dir, e, h := submittedSlot(t)
	if _, err := UpdateSlot(dir, "w1", func(s *Slot) { s.SetTask(""); s.SetBranch(""); _ = s.MarkState("idle", "") }); err != nil {
		t.Fatal(err)
	}
	next := DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "another brief"), Task: "T2", Kind: "codex"}
	if res, err := e.Dispatch(bg, dir, next); err != nil && strings.Contains(err.Error(), "unresolved delivery") {
		t.Fatalf("fresh session blocked by an earlier delivery: %+v %v", res, err)
	}
	if !strings.Contains(h.sent, "another brief") {
		t.Errorf("new brief not delivered; last send %q", h.sent)
	}
}
