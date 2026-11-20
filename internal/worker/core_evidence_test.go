package worker

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/stamp"
)

// submittedSlot dispatches a task whose Core delivery ends "submitted" and
// returns the project, the env and the rig's host. The host then proves nothing
// further, so only consumption evidence can free the slot.
func submittedSlot(t *testing.T) (string, Env, *fakeHost) {
	t.Helper()
	dir := gittest.Project(t, `{"round":{"dispatch":"herdr-core"}}`)
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

// relayDelivered: the relay went out as a new delivery, so the saved record is
// the relay's own, not the old task's.
func relayDelivered(t *testing.T, dir string, h *fakeHost) {
	t.Helper()
	p := corePending(LoadRegistryTolerant(dir).Slot("w1"))
	if p == nil || !strings.HasPrefix(jsonx.Str(p, "request"), "relay:") || !strings.Contains(h.sent, "answer once") {
		t.Fatalf("relay not recorded as the slot's delivery: record %v, last send %q", p, h.sent)
	}
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

// gitIn runs real git in dir, with commits dated at when.
func gitIn(t *testing.T, dir string, when time.Time, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(), "GIT_COMMITTER_DATE="+when.Format(time.RFC3339), "GIT_AUTHOR_DATE="+when.Format(time.RFC3339),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// branchWithCommit makes the slot's branch carry one commit past main, dated
// when, locally and, when pushed, on the remote-tracking ref too.
func branchWithCommit(t *testing.T, dir string, when time.Time, pushed, tip bool) {
	t.Helper()
	sha := gitIn(t, dir, when, "commit-tree", "main^{tree}", "-p", "main", "-m", "work")
	gitIn(t, dir, when, "update-ref", "refs/heads/w1/t1-x", sha)
	if pushed {
		gitIn(t, dir, when, "update-ref", "refs/remotes/origin/w1/t1-x", sha)
	}
	if _, err := UpdateSlot(dir, "w1", func(s *Slot) {
		s.SetBranch("w1/t1-x")
		s.o.Set("base", "main")
		p := corePending(s)
		if tip {
			p.Set("remoteTip", "absent") // the ref did not exist when the delivery was saved
		} else {
			p.Delete("remoteTip") // a delivery saved before remoteTip existed
		}
	}); err != nil {
		t.Fatal(err)
	}
}

// #3320 AC-1: publication after the delivery is the evidence, whatever the
// commit date: a commit made before the delivery and pushed after it clears.
// The real git calls run: no stub.
func TestCoreDeliveryClearedByCommitPushedAfterDelivery(t *testing.T) {
	dir, e, h := submittedSlot(t)
	branchWithCommit(t, dir, e.Now().Add(-time.Hour), true, true)
	e.Dispatch(bg, dir, relayOpts(t))
	relayDelivered(t, dir, h)
}

// A delivery saved before remoteTip existed keeps the commit-date rule.
func TestCoreLegacyDeliveryUsesCommitDate(t *testing.T) {
	dir, e, h := submittedSlot(t)
	branchWithCommit(t, dir, e.Now().Add(time.Hour), true, false)
	e.Dispatch(bg, dir, relayOpts(t))
	relayDelivered(t, dir, h)
}

// A local commit nobody pushed is not consumption evidence, however new.
func TestCoreDeliveryKeptByUnpushedCommit(t *testing.T) {
	for name, tip := range map[string]bool{"with-tip": true, "legacy": false} {
		t.Run(name, func(t *testing.T) {
			dir, e, _ := submittedSlot(t)
			branchWithCommit(t, dir, e.Now().Add(time.Hour), false, tip)
			if _, err := e.Dispatch(bg, dir, relayOpts(t)); err == nil || !strings.Contains(err.Error(), "unresolved delivery (state: submitted)") {
				t.Fatalf("an unpushed commit must not clear the delivery: %v", err)
			}
		})
	}
}

// #3320 AC-1: a ROTA-DONE the poll reads past the busy edge spends the delivery.
func TestPollRecordedDoneClearsCoreDelivery(t *testing.T) {
	dir, e, h := submittedSlot(t)
	h.panes = map[string][]string{"w1": {"x\nROTA-DONE w1 https://github.com/o/r/pull/9\n", "x\nROTA-DONE w1 https://github.com/o/r/pull/9\n"}}
	if _, err := UpdateSlot(dir, "w1", func(s *Slot) { _ = s.MarkState("busy", "") }); err != nil {
		t.Fatal(err)
	}
	if !hasCoreDelivery(dir) {
		t.Fatal("setup: no saved delivery")
	}
	if _, err := e.Poll(bg, dir, PollOpts{Lines: 60}); err != nil {
		t.Fatal(err)
	}
	if got := slotField(t, dir, "w1", "state"); got != "done" {
		t.Fatalf("poll did not record done: %s", got)
	}
	if hasCoreDelivery(dir) {
		t.Fatal("a poll-recorded ROTA-DONE left the delivery behind")
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

// #3320 AC-3: a task dispatch under another harness is a fresh session even
// with the same task and brief; it never replays the old harness's payload.
func TestCoreHarnessChangeIsAFreshSession(t *testing.T) {
	dir, e, h := submittedSlot(t)
	if _, err := UpdateSlot(dir, "w1", func(s *Slot) { s.SetTask(""); s.SetBranch(""); _ = s.MarkState("idle", "") }); err != nil {
		t.Fatal(err)
	}
	before := 0
	for _, c := range h.calls {
		if strings.HasPrefix(c, "spawn ") {
			before++
		}
	}
	// Same task and brief as submittedSlot, other harness.
	same := DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "work once"), Task: "T1", Kind: "claude"}
	_, err := e.Dispatch(bg, dir, same)
	spawns := 0
	for _, c := range h.calls {
		if strings.HasPrefix(c, "spawn ") {
			spawns++
		}
	}
	if spawns != before+1 {
		t.Fatalf("a harness change must spawn a fresh session: %d -> %d spawns (err %v)", before, spawns, err)
	}
}

// #3320: a done the poll only inferred (idle pane, open PR on the slot's branch:
// pollTarget.promoted) is not the worker answering and leaves the delivery; a
// real ROTA-DONE past the busy edge clears it.
func TestRecordRowPromotedDoneKeepsCoreDelivery(t *testing.T) {
	for _, promoted := range []bool{true, false} {
		s := NewSlot("w1", "b", "wt", "main", "h")
		s.MarkState("busy", "")
		d := jsonx.NewObject()
		d.Set("state", "submitted")
		s.o.Set("coreDelivery", d)
		tgt := slotTarget(s)
		tgt.promoted = promoted
		if _, err := recordRow(s, PollRow{"w1", StateDone, "ROTA-DONE"}, tgt, time.Now(), Registry{}); err != nil {
			t.Fatal(err)
		}
		if kept := corePending(s) != nil; kept != promoted {
			t.Errorf("promoted=%v: delivery kept=%v", promoted, kept)
		}
	}
}

// #3320: a promoted done keeps the delivery; the next poll's real ROTA-DONE,
// with no busy edge between and no relay pending, still clears it.
func TestRecordRowRealDoneAfterPromotedDoneClearsCoreDelivery(t *testing.T) {
	s := NewSlot("w1", "b", "wt", "main", "h")
	s.MarkState("busy", "")
	d := jsonx.NewObject()
	d.Set("state", "submitted")
	s.o.Set("coreDelivery", d)
	tgt := slotTarget(s)
	tgt.promoted = true
	if _, err := recordRow(s, PollRow{"w1", StateDone, "ROTA-DONE"}, tgt, time.Now(), Registry{}); err != nil {
		t.Fatal(err)
	}
	if corePending(s) == nil {
		t.Fatal("a promoted done cleared the delivery")
	}
	tgt = slotTarget(s)
	if _, err := recordRow(s, PollRow{"w1", StateDone, "ROTA-DONE"}, tgt, time.Now(), Registry{}); err != nil {
		t.Fatal(err)
	}
	if corePending(s) != nil {
		t.Fatal("a real ROTA-DONE after a promoted done left the delivery behind")
	}
}

// #3320: the remote tip is snapshotted for the branch the dispatch moves the
// slot onto, not the one it held before. The new task branch is already on the
// remote and nothing is pushed after the delivery: still refused.
func TestCoreDispatchSnapshotsTheDispatchedBranchTip(t *testing.T) {
	dir := gittest.Project(t, `{"round":{"dispatch":"herdr-core"}}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	sha := gitIn(t, dir, time.Now(), "rev-parse", "main")
	gitIn(t, dir, time.Now(), "update-ref", "refs/remotes/origin/"+BranchFor("w1", "T1"), sha)
	h := &fakeHost{receipts: true, name: host.HerdrCore, sendErr: host.ErrSubmitted}
	rig := &codexRig{loggedIn: true, missing: map[string]bool{"herdr": true}}
	e := rig.env(h)
	task := DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "work once"), Task: "T1", Kind: "codex"}
	if res, err := e.Dispatch(bg, dir, task); err != nil || res.Delivery != "submitted" {
		t.Fatalf("task: %+v %v, want submitted", res, err)
	}
	s := LoadRegistryTolerant(dir).Slot("w1")
	if got := jsonx.Str(corePending(s), "remoteTip"); got != sha {
		t.Fatalf("remoteTip = %q, want the dispatched branch's tip %q", got, sha)
	}
	if _, err := e.Dispatch(bg, dir, relayOpts(t)); err == nil || !strings.Contains(err.Error(), "unresolved delivery (state: submitted)") {
		t.Fatalf("nothing was pushed after the delivery: relay must still refuse: %v", err)
	}
}

// #3320, through Wait: an idle pane with an open PR is promoted to DONE and
// keeps the delivery; the worker's own ROTA-DONE for that same PR, with no busy
// read between, has the same seen key. It is no news, but it still answers the
// delivery.
func TestWaitRealDoneAfterPromotedDoneClearsCoreDelivery(t *testing.T) {
	w := newWorld(t, "")
	w.forge("listed", "1")
	if err := Update(w.dir, func(d *Doc) {
		s := d.Slot("w1")
		s.SetHandle("w9:t1")
		s.MarkState("busy", stamp.Format(time.Now()))
		del := jsonx.NewObject()
		del.Set("state", "submitted")
		s.o.Set("coreDelivery", del)
		s.ClearSeen()
	}); err != nil {
		t.Fatal(err)
	}
	h := newWaitHost("herdr")
	h.set("w1", "", "idle")
	e := w.env(false)
	e.NewHost = func(host.Kind) host.Host { return watcherHost{h} }
	res, err := e.Wait(bg, w.dir, WaitOpts{Timeout: 5 * time.Second})
	if err != nil || res.State != StateDone || res.Evidence != ghURL || res.Note != missingSentinelNote {
		t.Fatalf("%+v %v, want a promoted DONE", res, err)
	}
	if !hasCoreDelivery(w.dir) {
		t.Fatal("a promoted done cleared the delivery")
	}
	h.set("w1", "ROTA-DONE w1 "+ghURL+"\n", "done")
	res, err = e.Wait(bg, w.dir, WaitOpts{Timeout: 50 * time.Millisecond})
	if err != nil || !res.TimedOut {
		t.Fatalf("%+v %v: the same DONE came back as news", res, err)
	}
	if hasCoreDelivery(w.dir) {
		t.Error("the worker's ROTA-DONE after a promoted done left the delivery behind")
	}
	if LoadRegistryTolerant(w.dir).Slot("w1").DonePromoted() {
		t.Error("the promoted mark outlived the real done")
	}
}

// #3832 AC-1/AC-2: a relay for a queued PR was acted on through the PR's own
// branch while the slot moved on to another issue. The push to the delivery's
// target branch is pickup, not the slot's current branch.
func TestCoreDeliveryClearedByPushToRelayTargetBranch(t *testing.T) {
	dir, e, h := submittedSlot(t)
	when := e.Now().Add(-time.Hour)
	sha := gitIn(t, dir, when, "commit-tree", "main^{tree}", "-p", "main", "-m", "fix")
	gitIn(t, dir, when, "update-ref", "refs/remotes/origin/w1/a-old", sha)
	if _, err := UpdateSlot(dir, "w1", func(s *Slot) {
		s.SetBranch("w1/b-current") // the slot returned to another issue
		s.o.Set("base", "main")
		p := corePending(s)
		p.Set("branch", "w1/a-old") // the relay's target, recorded at send
		p.Set("remoteTip", "absent")
	}); err != nil {
		t.Fatal(err)
	}
	e.Dispatch(bg, dir, relayOpts(t))
	relayDelivered(t, dir, h)
}

// The same slot with no push to the target stays blocked.
func TestCoreDeliveryKeptWithoutPushToRelayTargetBranch(t *testing.T) {
	dir, e, _ := submittedSlot(t)
	if _, err := UpdateSlot(dir, "w1", func(s *Slot) {
		s.SetBranch("w1/b-current")
		p := corePending(s)
		p.Set("branch", "w1/a-old")
		p.Set("remoteTip", "absent")
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Dispatch(bg, dir, relayOpts(t)); err == nil || !strings.Contains(err.Error(), "unresolved delivery (state: submitted)") {
		t.Fatalf("no push to the target must keep the delivery: %v", err)
	}
}
