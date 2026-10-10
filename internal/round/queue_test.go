package round

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/tracker"
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

func (f *moveFx) queued() []worker.QueuedPR { return worker.LoadRegistryTolerant(f.root).PRs() }

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
	if f.be.claims["12"] != "ben@1" || f.be.bstates["12"] != "in-progress" {
		t.Errorf("the queued issue stays taken: %v %v", f.be.claims, f.be.bstates)
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
	if _, err := f.env.Assign(bg, f.root, f.be, AssignOpts{ID: "13", Agent: "ben", HolderPID: 100, Settings: f.set, CheckOnly: true}); err != nil {
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
	f.env.Forge = (&fakeRemote{labelled: []int{12, 13}}).asForge()
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
	f.env.Forge = (&fakeRemote{labelled: []int{12, 13}, states: map[int]string{7: "merged"}}).asForge()
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
	f.env.Forge = (&fakeRemote{labelled: []int{12, 13}}).asForge()
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
	if len(f.queued()) != 0 || f.be.claims["12"] != "" || f.be.bstates["12"] != "" {
		t.Errorf("record %v claims %v states %v", f.queued(), f.be.claims, f.be.bstates)
	}
	if got := f.forge.labels[12]; len(got) != 1 || got[0] != DefaultNeedsHuman {
		t.Errorf("labels: %v", f.forge.labels)
	}
	if f.slot("ben").Task() != "13" {
		t.Errorf("ben must keep its new issue: %v", f.slot("ben"))
	}
}

// mintReview makes issue id a review item this round minted, held by ben.
func (f *moveFx) mintReview(t *testing.T, id string) {
	t.Helper()
	f.be.items[id].Title = ReviewTitle("cli")
	if err := worker.Update(f.root, func(d *worker.Doc) { d.RecordReviewItems([]string{id}) }); err != nil {
		t.Fatal(err)
	}
}

func (f *moveFx) reportIssues(t *testing.T, slot string, refs ...string) {
	t.Helper()
	if err := editSlot(f.root, slot, func(s *worker.Slot) error {
		s.MarkState("done", "")
		s.SetIssues(refs)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAssignOntoReviewSlotClosesTheReviewItem(t *testing.T) {
	f := newMoveFx(t)
	f.mintReview(t, "12")
	f.reportIssues(t, "ben", "#139", "#140")

	res, err := f.assign("13", "ben")
	if err != nil || !res.Dispatched {
		t.Fatalf("%+v %v", res, err)
	}
	if !f.be.items["12"].Closed || strings.Join(f.be.comments["12"][len(f.be.comments["12"])-2:], "|") != "Review done: filed #139, #140.|closed: filed #139, #140" {
		t.Errorf("the review item closes with the issues listed: %v %v", f.be.items["12"].Closed, f.be.comments["12"])
	}
	if f.be.claims["12"] != "" || f.be.bstates["12"] == "in-progress" {
		t.Errorf("claim and state are released: %v %v", f.be.claims, f.be.bstates)
	}
	if len(f.queued()) != 0 {
		t.Errorf("no PR is queued: %v", f.queued())
	}
	if s := f.slot("ben"); s.Task() != "13" || len(s.Issues()) != 0 {
		t.Errorf("slot: %v", s)
	}
}

func TestReportedIssuesOnAnOrdinaryItemDoNotFreeTheSlot(t *testing.T) {
	f := newMoveFx(t)
	f.reportIssues(t, "ben", "#139")
	_, err := f.assign("13", "ben")
	if blockedBy(t, err) != BlockSlotBusy || !strings.Contains(err.Error(), "not an architecture review item") {
		t.Fatalf("%v", err)
	}
	if f.be.items["12"].Closed || f.slot("ben").Task() != "12" {
		t.Error("a refusal moves nothing")
	}
	// a title alone is not trusted either: the registry must have minted it
	f.be.items["12"].Title = ReviewTitle("cli")
	if _, err := f.assign("13", "ben"); blockedBy(t, err) != BlockSlotBusy || f.be.items["12"].Closed {
		t.Errorf("unminted review title: %v", err)
	}
}

func TestOverlapSkipsClosedIssues(t *testing.T) {
	t.Run("target slot's own open unmerged issue still overlaps", func(t *testing.T) {
		f := newMoveFx(t)
		f.finish(t, "ben", pr7)
		if _, err := f.assign("14", "ben"); blockedBy(t, err) != BlockOverlap {
			t.Fatalf("ben's PR is open, so 12 is still live: %v", err)
		}
	})
	t.Run("closed issue", func(t *testing.T) {
		f := newMoveFx(t)
		f.forge.items["12"].Closed = true
		if _, err := f.assign("14", "dana"); err != nil {
			t.Fatalf("a closed issue never overlaps: %v", err)
		}
	})
	t.Run("live overlap with another slot", func(t *testing.T) {
		f := newMoveFx(t)
		if _, err := f.assign("14", "dana"); blockedBy(t, err) != BlockOverlap {
			t.Fatalf("12 is live on ben: %v", err)
		}
	})
	t.Run("busy target slot still refused", func(t *testing.T) {
		f := newMoveFx(t)
		if _, err := f.assign("14", "ben"); blockedBy(t, err) != BlockSlotBusy {
			t.Fatalf("ben is mid-work, not parkable: %v", err)
		}
	})
}

// A queued PR whose issue has an empty body still carries the issue's
// Subsystem scope through InFlightItems' queue branch.
func TestInFlightQueuedEmptyBodyCarriesSubsystem(t *testing.T) {
	f := newMoveFx(t)
	f.be.details["12"] = ""
	f.be.items["12"].Fields.Subsystem = "CLI"
	f.finish(t, "ben", pr7)
	if _, err := f.assign("13", "ben"); err != nil {
		t.Fatal(err)
	}
	var got []InFlight
	for _, x := range f.env.InFlightItems(bg, f.root, f.be, f.env.trackedFiles(bg, f.root), nil) {
		if x.Slot == "queue:ben" {
			got = append(got, x)
		}
	}
	if len(got) != 1 || !reflect.DeepEqual(got[0].Scopes, []string{"subsystem:cli"}) {
		t.Errorf("queued item scopes: %+v", got)
	}
}

// A slot's pr can be re-recorded stale: poll reads the last ROTA-DONE in the
// pane, which is still the previous issue's while the new issue runs. The queue
// must not pair that PR with the slot's newer issue and branch (#648).
func TestQueueKeepsARecordWhenTheSlotRecordsItsPRAgain(t *testing.T) {
	f := newMoveFx(t)
	branchA := f.slot("ben").Branch()
	f.finish(t, "ben", pr7)
	if _, err := f.assign("13", "ben"); err != nil {
		t.Fatal(err)
	}
	f.finish(t, "ben", pr7) // the stale sentinel of issue 12, now on issue 13's slot
	f.be.add("15", "Fourth issue", "M01", false, "## Acceptance\n- [ ] ok\n\nedits internal/fourth.go")
	f.be.items["15"].Number = 15
	if _, err := f.assign("15", "ben"); blockedBy(t, err) != BlockSlotBusy || !strings.Contains(err.Error(), "already queued") {
		t.Errorf("a PR queued for another issue is not this slot's PR: %v", err)
	}
	q := f.queued()
	if len(q) != 1 || q[0].Issue != "12" || q[0].Branch != branchA || q[0].PR != pr7 {
		t.Errorf("the record for PR 7 keeps its issue and branch: %v", q)
	}
}

func TestReconcileReportsAQueuedRecordWhoseBranchDoesNotHeadItsPR(t *testing.T) {
	f := newAssignFixture(t)
	worker.Update(f.root, func(d *worker.Doc) {
		d.QueuePR(worker.QueuedPR{Issue: "13", PR: pr7, From: "ben", Branch: "ben/13-x"})
		d.QueuePR(worker.QueuedPR{Issue: "14", PR: "https://github.com/o/r/pull/8", From: "dana", Branch: "dana/14-x"})
	})
	f.env.Forge = (&fakeRemote{
		states:  map[int]string{7: "open", 8: "open"},
		prViews: map[int]tracker.PRInfo{7: {Head: "ben/12-x"}, 8: {Head: "dana/14-x"}},
	}).asForge()
	out, err := f.env.Reconcile(bg, f.root, true)
	if err != nil {
		t.Fatal(err)
	}
	var got []Finding
	for _, d := range out.Drift {
		if d.Kind == QueuedBranchMismatch {
			got = append(got, d)
		}
	}
	if len(got) != 1 || got[0].Issue != "13" || !strings.Contains(got[0].Detail, "ben/12-x") {
		t.Errorf("only the mismatched record is reported: %+v", out.Drift)
	}
	if len(worker.LoadRegistryTolerant(f.root).PRs()) != 2 {
		t.Error("reported, not repaired")
	}
}
