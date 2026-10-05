package round

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/worker"
)

const pr7 = "https://github.com/o/r/pull/7"

// finish records a slot as done with an open PR, the state `round wait` leaves.
func (f *moveFx) finish(t *testing.T, slot, pr string) {
	t.Helper()
	if err := rawSlot(f.root, slot, func(s *jsonx.Object) {
		s.Set("state", "done")
		s.Set("pr", pr)
	}); err != nil {
		t.Fatal(err)
	}
}

func (f *moveFx) queued() []worker.QueuedPR { return worker.LoadRegistry(f.root).PRs() }

func TestAssignOntoDoneSlotQueuesItsPR(t *testing.T) {
	f := newMoveFx(t)
	branch := f.slot("ben").Branch()
	rawSlot(f.root, "ben", func(s *jsonx.Object) {
		s.Set("relays", []any{map[string]any{"round": 1}})
	})
	f.finish(t, "ben", pr7)

	res, err := f.assign("13", "ben")
	if err != nil || !res.Dispatched || res.Agent != "ben" {
		t.Fatalf("%+v %v", res, err)
	}
	q := f.queued()
	if len(q) != 1 {
		t.Fatalf("one record, have %v", q)
	}
	r := q[0]
	if r.Issue != "12" || r.Branch != branch || r.PR != pr7 || r.From != "ben" || r.ClaimID != "ben@1" || r.Base != "main" {
		t.Errorf("record: %v", r)
	}
	if len(r.Relays) != 1 {
		t.Errorf("relays are copied: %v", r)
	}
	s := f.slot("ben")
	if s.Task() != "13" || s.ClaimID() != "ben@1" || s.PR() != "" || s.State() != "busy" {
		t.Errorf("slot: %v", s)
	}
	if f.be.claims["12"] != "ben@1" || f.be.states["12"] != "in-progress" {
		t.Errorf("the queued issue stays taken: %v %v", f.be.claims, f.be.states)
	}
	if !f.remoteHas(t, branch) {
		t.Error("the queued branch is pushed")
	}
	if got := f.be.comments["12"]; len(got) != 0 && strings.Contains(strings.Join(got, ""), "rota handoff") {
		t.Errorf("no handoff comment: %v", got)
	}
	if held := heldIDs(f.root); !held["12"] || !held["13"] {
		t.Errorf("held: %v", held)
	}
}

// After a mid-round `migrate issues` a slot may still hold `B30`; its mapped
// issue number counts as held, so candidates does not offer it (#27).
func TestHeldIDsCountAMappedFileModeID(t *testing.T) {
	f := newMoveFx(t)
	rawSlot(f.root, "ben", func(s *jsonx.Object) { s.Set("task", "B30") })
	if held := heldIDs(f.root); held["17"] {
		t.Fatalf("no map yet: %v", held)
	}
	os.WriteFile(filepath.Join(f.root, ".rota", "issue-map.json"),
		[]byte(`{"B30":{"id":"B17","number":17,"url":"u","done":[]}}`), 0o644)
	if held := heldIDs(f.root); !held["B30"] || !held["17"] {
		t.Errorf("held: %v", held)
	}
}

func TestAssignRefusesBusyOrDirtySlot(t *testing.T) {
	f := newMoveFx(t)
	rawSlot(f.root, "ben", func(s *jsonx.Object) { s.Set("pr", pr7) }) // state still busy
	_, err := f.assign("13", "ben")
	if by := blockedBy(t, err); by != BlockSlotBusy || !strings.Contains(err.Error(), "slot ben holds 12 (busy") {
		t.Errorf("busy: %v", err)
	}
	f.finish(t, "ben", "")
	if _, err := f.assign("13", "ben"); blockedBy(t, err) != BlockSlotBusy || !strings.Contains(err.Error(), "no PR recorded") {
		t.Errorf("done without a PR: %v", err)
	}
	f.finish(t, "ben", pr7)
	os.WriteFile(filepath.Join(f.wt("ben"), "dirty.txt"), []byte("x"), 0o644)
	if _, err := f.assign("13", "ben"); blockedBy(t, err) != BlockSlotBusy || !strings.Contains(err.Error(), "uncommitted") {
		t.Errorf("dirty: %v", err)
	}
	if len(f.queued()) != 0 || f.slot("ben").Task() != "12" {
		t.Errorf("a refusal moves nothing: %v %v", f.queued(), f.slot("ben"))
	}
}

func TestAssignRefusalMovesNothingBeforeTheClaim(t *testing.T) {
	f := newMoveFx(t)
	f.finish(t, "ben", pr7)
	f.set.Brief = filepath.Join(f.root, "nope.md")
	if _, err := f.assign("13", "ben"); blockedBy(t, err) != BlockBriefMissing {
		t.Fatalf("%v", err)
	}
	if len(f.queued()) != 0 || f.slot("ben").Task() != "12" {
		t.Errorf("a refused assign parks nothing: %v", f.queued())
	}
	// CheckOnly never parks either.
	f.set.Brief = ""
	if _, err := f.env.Assign(bg, f.root, f.be, AssignOpts{ID: "13", Agent: "ben", HolderPID: 100, Settings: f.set, CheckOnly: true, Getenv: func(string) string { return "" }}); err != nil {
		t.Fatal(err)
	}
	if len(f.queued()) != 0 || f.slot("ben").Task() != "12" {
		t.Errorf("check-only parks nothing: %v", f.queued())
	}
}

func TestAssignWithoutAgentPrefersAnEmptySlot(t *testing.T) {
	f := newMoveFx(t)
	f.finish(t, "ben", pr7)
	res, err := f.assign("13", "")
	if err != nil || res.Agent != "dana" || len(f.queued()) != 0 {
		t.Fatalf("an empty slot first: %+v %v %v", res, err, f.queued())
	}
	f.be.add("16", "Other", "M01", false, "## Acceptance\n- [ ] ok\n\nedits internal/other2.go")
	res, err = f.assign("16", "")
	if err != nil || res.Agent != "ben" || len(f.queued()) != 1 {
		t.Fatalf("then the parkable one: %+v %v %v", res, err, f.queued())
	}
	// Both slots now hold running work.
	f.be.add("15", "Fourth", "M01", false, "## Acceptance\n- [ ] ok")
	if _, err := f.assign("15", ""); blockedBy(t, err) != BlockNoFreeSlot {
		t.Errorf("no free slot: %v", err)
	}
}

func TestAssignMergedPRLeavesNoRecord(t *testing.T) {
	f := newMoveFx(t)
	f.forge.states = map[int]string{7: "merged"}
	f.finish(t, "ben", pr7)
	if _, err := f.assign("13", "ben"); err != nil {
		t.Fatal(err)
	}
	if len(f.queued()) != 0 {
		t.Errorf("merged work is in: %v", f.queued())
	}
	if f.slot("ben").Task() != "13" {
		t.Errorf("slot: %v", f.slot("ben"))
	}
}

func TestQueuedIssueIsHeldAndNotAssignable(t *testing.T) {
	f := newMoveFx(t)
	f.finish(t, "ben", pr7)
	if _, err := f.assign("13", "ben"); err != nil {
		t.Fatal(err)
	}
	cands, err := f.env.Candidates(bg, f.root, f.be, CandidateOpts{Scope: roundcfg.ScopeMilestone})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cands {
		if c.ID == "12" || c.ID == "13" {
			t.Errorf("%s is held, not a candidate", c.ID)
		}
	}
	if _, err := f.assign("12", "dana"); blockedBy(t, err) != BlockClaimed || !strings.Contains(err.Error(), "transfer") {
		t.Errorf("a queued issue is not assignable: %v", err)
	}
	// Its footprint and branch count for overlap: 14 names the same file as 12.
	var inFlight []InFlight
	for _, x := range f.env.InFlightItems(bg, f.root, f.be, f.env.trackedFiles(bg, f.root), nil) {
		if x.Issue == "12" {
			inFlight = append(inFlight, x)
		}
	}
	if len(inFlight) != 1 || inFlight[0].Slot != "queue:ben" || !hasKind(inFlight[0].Paths, "ben-work.txt") {
		t.Errorf("in flight: %+v", inFlight)
	}
}

func TestStatusReportsQueuedPRs(t *testing.T) {
	f := newMoveFx(t)
	f.finish(t, "ben", pr7)
	if _, err := f.assign("13", "ben"); err != nil {
		t.Fatal(err)
	}
	f.env.Forge = &fakeForge{labelled: []int{12, 13}}
	rep, err := f.env.Status(bg, f.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Queued) != 1 || rep.Queued[0] != (QueuedPR{Issue: "12", PR: pr7, Branch: f.queued()[0].Branch, From: "ben"}) {
		t.Errorf("queued: %+v", rep.Queued)
	}
	for _, fd := range rep.Findings {
		if fd.Kind == LabelOrphan || fd.Kind == PRStale {
			t.Errorf("a queued issue is held: %+v", fd)
		}
	}

	// Merged on the forge: a finding with a repair that drops the record.
	f.env.Forge = &fakeForge{labelled: []int{12, 13}, states: map[int]string{7: "merged"}}
	out, err := f.env.Reconcile(bg, f.root, true)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, fd := range out.Repaired {
		if fd.Kind == PRStale && fd.Issue == "12" && strings.Contains(fd.Detail, "PR #7 in review is merged") {
			found = true
		}
	}
	if !found || len(f.queued()) != 0 {
		t.Errorf("repair: %+v queued %v", out, f.queued())
	}
}

func TestClaimFindingsTreatAQueuedRecordLikeASlot(t *testing.T) {
	f := newMoveFx(t)
	f.finish(t, "ben", pr7)
	if _, err := f.assign("13", "ben"); err != nil {
		t.Fatal(err)
	}
	f.env.Board = f.be
	f.env.Forge = &fakeForge{labelled: []int{12, 13}}
	rep, err := f.env.Status(bg, f.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, fd := range rep.Findings {
		if fd.Kind == ClaimMismatch {
			t.Errorf("matching claim: %+v", fd)
		}
	}
	delete(f.be.claims, "12")
	rep, _ = f.env.Status(bg, f.root)
	n := 0
	for _, fd := range rep.Findings {
		if fd.Kind == ClaimMismatch && fd.Issue == "12" && fd.Repair == "" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("a record whose claim is gone is flagged once: %+v", rep.Findings)
	}
}

func TestTransferToParkableReceiverQueuesIt(t *testing.T) {
	f := newMoveFx(t)
	if _, err := f.assign("13", "dana"); err != nil {
		t.Fatal(err)
	}
	f.commit(t, "dana", "dana-work.txt")
	f.finish(t, "dana", "https://github.com/o/r/pull/8")
	res, err := f.transfer("12", "dana", nil)
	if err != nil || !res.Dispatched || res.From != "ben" {
		t.Fatalf("%+v %v", res, err)
	}
	q := f.queued()
	if len(q) != 1 || q[0].Issue != "13" || q[0].From != "dana" {
		t.Errorf("dana's PR is queued: %v", q)
	}
	if f.slot("dana").Task() != "12" || f.slot("ben").Task() != "" {
		t.Errorf("slots: %v %v", f.slot("dana"), f.slot("ben"))
	}
}

func TestTransferFromAQueuedRecord(t *testing.T) {
	f := newMoveFx(t)
	branch := f.slot("ben").Branch()
	rawSlot(f.root, "ben", func(s *jsonx.Object) {
		s.Set("relays", []any{map[string]any{"round": 1, "summary": "use the lease"}})
	})
	f.finish(t, "ben", pr7)
	if _, err := f.assign("13", "ben"); err != nil { // queues 12, ben moves on
		t.Fatal(err)
	}
	head := gitIn(t, f.root, "log", "-1", "--abbrev=7", "--format=%h %s", "origin/"+branch)

	res, err := f.transfer("12", "dana", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Dispatched || res.From != "ben" || res.Branch != branch || res.Head != head || res.ClaimID != "dana@1" {
		t.Fatalf("%+v", res)
	}
	if len(f.queued()) != 0 {
		t.Errorf("the record is consumed: %v", f.queued())
	}
	s := f.slot("dana")
	if s.Task() != "12" || s.PR() != pr7 || s.Branch() != branch || s.ClaimID() != "dana@1" {
		t.Errorf("dana: %v", s)
	}
	if v, _ := s.Raw().Get("relays"); len(v.([]any)) != 1 {
		t.Errorf("relays carry over: %v", s)
	}
	if cur := gitIn(t, f.wt("dana"), "symbolic-ref", "--short", "HEAD"); cur != branch {
		t.Errorf("dana's worktree sits on %s, not %s", branch, cur)
	}
	if f.be.claims["12"] != "dana@1" {
		t.Errorf("claim: %v", f.be.claims)
	}
	// ben is untouched: it holds 13.
	if f.slot("ben").Task() != "13" {
		t.Errorf("ben: %v", f.slot("ben"))
	}
	if got := f.be.comments["12"]; len(got) != 2 || !strings.Contains(got[1], "from ben") || !strings.Contains(got[1], head) {
		t.Errorf("handoff: %v", got)
	}
}

func TestTransferFromAQueuedRecordToHuman(t *testing.T) {
	f := newMoveFx(t)
	f.finish(t, "ben", pr7)
	if _, err := f.assign("13", "ben"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.transfer("12", HumanTarget, nil); err != nil {
		t.Fatal(err)
	}
	if len(f.queued()) != 0 || f.be.claims["12"] != "" || f.be.states["12"] != "" {
		t.Errorf("record %v claims %v states %v", f.queued(), f.be.claims, f.be.states)
	}
	if got := f.forge.labels[12]; len(got) != 1 || got[0] != DefaultNeedsHuman {
		t.Errorf("labels: %v", f.forge.labels)
	}
	if f.slot("ben").Task() != "13" {
		t.Errorf("ben must keep its new issue: %v", f.slot("ben"))
	}
}
