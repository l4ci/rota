package worker

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
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
func branchWithCommit(t *testing.T, dir string, when time.Time, pushed bool) {
	t.Helper()
	sha := gitIn(t, dir, when, "commit-tree", "main^{tree}", "-p", "main", "-m", "work")
	gitIn(t, dir, when, "update-ref", "refs/heads/w1/t1-x", sha)
	if pushed {
		gitIn(t, dir, when, "update-ref", "refs/remotes/origin/w1/t1-x", sha)
	}
	if _, err := UpdateSlot(dir, "w1", func(s *Slot) { s.SetBranch("w1/t1-x"); s.o.Set("base", "main") }); err != nil {
		t.Fatal(err)
	}
}

// #3320 AC-1: a commit pushed to the slot's branch after the delivery is
// evidence. The real git call runs: no stub.
func TestCoreDeliveryClearedByLaterPushedCommit(t *testing.T) {
	dir, e, h := submittedSlot(t)
	branchWithCommit(t, dir, e.Now().Add(time.Hour), true)
	e.Dispatch(bg, dir, relayOpts(t))
	relayDelivered(t, dir, h)
}

// A local commit nobody pushed is not consumption evidence, nor is a pushed one
// that predates the delivery.
func TestCoreDeliveryKeptByUnpushedOrOlderCommit(t *testing.T) {
	for name, tc := range map[string]struct {
		offset time.Duration
		pushed bool
	}{"unpushed-later": {time.Hour, false}, "pushed-earlier": {-time.Hour, true}} {
		t.Run(name, func(t *testing.T) {
			dir, e, _ := submittedSlot(t)
			branchWithCommit(t, dir, e.Now().Add(tc.offset), tc.pushed)
			if _, err := e.Dispatch(bg, dir, relayOpts(t)); err == nil || !strings.Contains(err.Error(), "unresolved delivery (state: submitted)") {
				t.Fatalf("%s commit must not clear the delivery: %v", name, err)
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
