package worker

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/verdict"
)

const intBranch = "integration/r55-a"

// integrationPR runs a green integration train of #7 and #8 and records PR
// #9 from the branch to main; the forge's primary PR is #9 from then on. It
// returns the pushed tip.
func integrationPR(t *testing.T, w *world) string {
	t.Helper()
	w.forge("head#7", "w1")
	w.forge("sha#7", w.forgeWord("sha"))
	if res, err := w.train(TrainOpts{Targets: []string{"7", "8"}, Integration: "a"}); err != nil || res.Verdict != GatePass {
		t.Fatalf("integration train: %+v %v", res, err)
	}
	if err := UpdateIntegration(w.dir, intBranch, func(it *Integration) { it.PR = "https://github.com/o/r/pull/9" }); err != nil {
		t.Fatal(err)
	}
	// PR #9 carries the body rota writes, with no test sections added to it.
	reg, err := LoadRegistry(w.dir)
	if err != nil {
		t.Fatal(err)
	}
	rec, _ := reg.IntegrationByBranch(intBranch)
	w.forge("body#9", IntegrationPRBody(rec))
	return w.pointPR9()
}

// pointPR9 points the forge's primary PR (#9) at the branch's current tip.
func (w *world) pointPR9() string {
	tip := strings.TrimSpace(gittest.Run(w.t, w.origin, "rev-parse", intBranch))
	w.forge("head", intBranch)
	w.forge("sha", tip)
	w.forge("head#9", intBranch)
	w.forge("sha#9", tip)
	return tip
}

// pushFix commits a fix on the integration branch from the worker clone.
func (w *world) pushFix(file string) string {
	w.t.Helper()
	gittest.Run(w.t, w.worker, "fetch", "-q", "origin", intBranch)
	gittest.Run(w.t, w.worker, "checkout", "-q", "-B", "fix", "FETCH_HEAD")
	if err := os.WriteFile(filepath.Join(w.worker, file), []byte("fix\n"), 0o644); err != nil {
		w.t.Fatal(err)
	}
	gittest.Run(w.t, w.worker, "add", file)
	gittest.Run(w.t, w.worker, "commit", "-q", "-m", "fix "+file)
	gittest.Run(w.t, w.worker, "push", "-q", "origin", "fix:"+intBranch)
	return strings.TrimSpace(gittest.Run(w.t, w.worker, "rev-parse", "HEAD"))
}

func (w *world) gateCheck(slot string) (GateResult, error) {
	return w.env(false).Gate(bg, w.dir, GateOpts{Slot: slot, Base: "main", CheckOnly: true, CheckReviews: true})
}

func reviewWorld(t *testing.T) *world {
	w := integrationWorld(t)
	w.setConfig(`{"ship":{"review":"light"},"test":{"full":["true"]}}`)
	return w
}

func TestIntegrationTipCleanNeedsNoReview(t *testing.T) {
	w := reviewWorld(t)
	integrationPR(t, w)
	res, err := w.gateCheck("9")
	if err != nil || !res.OK() {
		t.Fatalf("a tip of rota's merges only needs no review: %+v %v", res, err)
	}
	if !strings.Contains(strings.Join(res.Notes, "\n"), "INTEGRATION-REVIEW "+intBranch+" — only rota's merges") {
		t.Errorf("notes: %v", res.Notes)
	}
}

func TestIntegrationTipNeedsFixReview(t *testing.T) {
	w := reviewWorld(t)
	integrationPR(t, w)
	fix := w.pushFix("fix.txt")
	w.pointPR9()
	res, err := w.gateCheck("9")
	if err != nil || res.Verdict != GateReviewMissing || !strings.Contains(res.Err, "1 fix commit(s)") {
		t.Fatalf("an unreviewed fix must refuse: %+v %v", res, err)
	}
	// A review recorded at an older commit, before the fix, does not cover it.
	w.record(intBranch, w.forgeWord("sha#8"), verdict.ReviewQuality, verdict.ReviewSpec)
	if res, _ := w.gateCheck("9"); res.Verdict != GateReviewMissing {
		t.Fatalf("a review before the fix covered it: %+v", res)
	}
	w.record(intBranch, fix, verdict.ReviewQuality, verdict.ReviewSpec)
	if res, err := w.gateCheck("9"); err != nil || !res.OK() {
		t.Fatalf("the reviewed fix still refuses: %+v %v", res, err)
	}
}

func TestIntegrationVerdictBeforeCleanMainMerge(t *testing.T) {
	w := reviewWorld(t)
	integrationPR(t, w)
	fix := w.pushFix("fix.txt")
	w.record(intBranch, fix, verdict.ReviewQuality, verdict.ReviewSpec)
	// main moves; the rerun merges it on top of the reviewed fix.
	trainWrite(t, w, "later.txt", "later")
	gittest.Run(t, w.dir, "push", "-q", "origin", "main")
	if res, err := w.train(TrainOpts{Targets: []string{"7", "8"}, Integration: "a"}); err != nil || res.Verdict != GatePass {
		t.Fatalf("rerun: %+v %v", res, err)
	}
	w.pointPR9()
	if res, err := w.gateCheck("9"); err != nil || !res.OK() {
		t.Fatalf("a rota merge after the reviewed fix needs no new review: %+v %v", res, err)
	}
	// A fix after that merge does.
	w.pushFix("fix2.txt")
	w.pointPR9()
	if res, _ := w.gateCheck("9"); res.Verdict != GateReviewMissing {
		t.Fatalf("a later fix slipped through: %+v", res)
	}
}

// countingFull is a test.full that counts its runs in a file.
func countingFull(t *testing.T, w *world) string {
	count := filepath.Join(t.TempDir(), "count")
	w.setConfig(`{"test":{"full":["echo x >> ` + count + `"]}}`)
	return count
}

func intRuns(count string) int {
	b, _ := os.ReadFile(count)
	return strings.Count(string(b), "x")
}

func (w *world) landPR9() (GateResult, error) {
	return w.env(false).Gate(bg, w.dir, GateOpts{Slot: "9", Base: "main"})
}

func TestWorkerGateIntegrationSkipsVerify(t *testing.T) {
	w := integrationWorld(t)
	count := countingFull(t, w)
	integrationPR(t, w)
	before := intRuns(count)
	res, err := w.landPR9()
	if err != nil || res.Verdict != GatePass {
		t.Fatalf("landing: %+v %v", res, err)
	}
	if intRuns(count) != before || !strings.Contains(strings.Join(res.Notes, "\n"), "INTEGRATION-VERIFIED") {
		t.Fatalf("a verified tip on an unmoved main verified again (%d -> %d): %v", before, intRuns(count), res.Notes)
	}
	gittest.Run(t, w.dir, "pull", "-q", "origin", "main")
	if !w.onMain("work.txt") || !w.onMain("work2.txt") {
		t.Fatal("the members are not on main")
	}
	if !slices.Equal(res.IntegrationMembers, []string{"5", "6"}) {
		t.Errorf("member issues: %v", res.IntegrationMembers)
	}
	reg, _ := LoadRegistry(w.dir)
	if rec, _ := reg.IntegrationByBranch(intBranch); !rec.Landed {
		t.Errorf("record not landed: %+v", rec)
	}
}

func TestWorkerGateIntegrationReverifiesAfterMainMoved(t *testing.T) {
	w := integrationWorld(t)
	count := countingFull(t, w)
	integrationPR(t, w)
	trainWrite(t, w, "later.txt", "later")
	gittest.Run(t, w.dir, "push", "-q", "origin", "main")
	before := intRuns(count)
	res, err := w.landPR9()
	if err != nil || res.Verdict != GatePass {
		t.Fatalf("landing: %+v %v", res, err)
	}
	if intRuns(count) <= before || !strings.Contains(strings.Join(res.Notes, "\n"), "INTEGRATION-REVERIFY") {
		t.Fatalf("main moved but the tip was not verified again (%d -> %d): %v", before, intRuns(count), res.Notes)
	}
}

func TestWorkerGateIntegrationRefusesMovedMember(t *testing.T) {
	w := integrationWorld(t)
	integrationPR(t, w)
	gittest.Run(t, w.worker, "checkout", "-q", "w2")
	trainWrite2(t, w.worker, "more.txt")
	gittest.Run(t, w.worker, "push", "-q", "origin", "w2")
	res, err := w.landPR9()
	if err != nil || res.Verdict != GateStale || !strings.Contains(res.Err, "member 8 moved") {
		t.Fatalf("a moved member must refuse the landing: %+v %v", res, err)
	}
	if w.onMain("work.txt") {
		t.Fatal("something landed")
	}
}

func TestTrainIntegrationRemergesMovedMember(t *testing.T) {
	w := integrationWorld(t)
	integrationPR(t, w)
	gittest.Run(t, w.worker, "checkout", "-q", "w2")
	head := trainWrite2(t, w.worker, "more.txt")
	gittest.Run(t, w.worker, "push", "-q", "origin", "w2")
	w.forge("sha#8", head)
	res, err := w.train(TrainOpts{Targets: []string{"7", "8"}, Integration: "a"})
	if err != nil || res.Verdict != GatePass || len(res.Members) != 1 || res.Members[0].Target != "8" {
		t.Fatalf("the moved member merges again, alone: %+v %v", res, err)
	}
	reg, _ := LoadRegistry(w.dir)
	rec, _ := reg.IntegrationByBranch(intBranch)
	if m, _ := rec.Member("8"); m.Head != head || len(rec.RotaMerges) != 3 {
		t.Fatalf("record after the re-merge: %+v", rec)
	}
	w.pointPR9()
	if res, err := w.landPR9(); err != nil || res.Verdict != GatePass {
		t.Fatalf("landing after the re-merge: %+v %v", res, err)
	}
}

// trainWrite2 commits file in dir and returns the new head.
func trainWrite2(t *testing.T, dir, file string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(file+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, dir, "add", file)
	gittest.Run(t, dir, "commit", "-q", "-m", "add "+file)
	return strings.TrimSpace(gittest.Run(t, dir, "rev-parse", "HEAD"))
}

// A fix that triggers a review lens is covered only with that lens recorded
// too, not by the core reviews alone.
func TestIntegrationFixReviewNeedsLens(t *testing.T) {
	w := integrationWorld(t)
	w.setConfig(`{"ship":{"review":"light"},"review":{"lenses":{"security":{"paths":["fix.txt"]}}},"test":{"full":["true"]}}`)
	integrationPR(t, w)
	fix := w.pushFix("fix.txt")
	w.pointPR9()
	w.record(intBranch, fix, verdict.ReviewQuality, verdict.ReviewSpec)
	if res, _ := w.gateCheck("9"); res.Verdict != GateReviewMissing {
		t.Fatalf("the core reviews covered a lens-triggering fix: %+v", res)
	}
	w.record(intBranch, fix, verdict.LensKind("security"))
	if res, err := w.gateCheck("9"); err != nil || !res.OK() {
		t.Fatalf("the fix with its lens review still refuses: %+v %v", res, err)
	}
}

// A verify skipped for an integration tip binds the merge to that tip: a PR
// head pinned anywhere else is refused, since no verify saw it.
func TestWorkerGateIntegrationVerifiedTipBindsMerge(t *testing.T) {
	w := integrationWorld(t)
	integrationPR(t, w)
	res, err := w.env(false).Gate(bg, w.dir, GateOpts{Slot: "9", Base: "main", LandVerified: true, LandVerifiedTip: strings.Repeat("e", 40)})
	if err != nil || res.Verdict != GateBaseMoved || res.Changed || w.onMain("work.txt") {
		t.Fatalf("a head other than the verified tip landed: %+v %v", res, err)
	}
}

// An external member PR whose branch names no issue still closes the issue
// its body names: the record, the Closes lines and the landed members carry it.
func TestIntegrationExternalMemberIssueFromBody(t *testing.T) {
	w := integrationWorld(t)
	if err := os.WriteFile(RegistryPath(w.dir), []byte(`{"round":55,"slots":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	w.forge("head#7", "w1")
	w.forge("sha#7", w.forgeWord("sha"))
	w.forge("body#7", "Closes #5\n"+completePRSections)
	w.forge("body#8", "Closes #6\n"+completePRSections)
	res, err := w.train(TrainOpts{Targets: []string{"7", "8"}, Integration: "a"})
	if err != nil || res.Verdict != GatePass {
		t.Fatalf("integration train of external PRs: %+v %v", res, err)
	}
	reg, _ := LoadRegistry(w.dir)
	rec, _ := reg.IntegrationByBranch(intBranch)
	var issues []string
	for _, m := range rec.Members {
		issues = append(issues, strings.TrimPrefix(m.Issue, "#"))
	}
	if !slices.Equal(issues, []string{"5", "6"}) {
		t.Fatalf("member issues from the PR bodies: %v", issues)
	}
	body := IntegrationPRBody(rec)
	if !strings.Contains(body, "Closes #5") || !strings.Contains(body, "Closes #6") {
		t.Fatalf("closing lines: %s", body)
	}
}

// A forge merge whose local confirmation failed is recovered by the retry even
// when a member branch was deleted after the merge (auto-delete of heads).
func TestWorkerGateIntegrationRecoversMergeWithDeletedMemberBranch(t *testing.T) {
	w := integrationWorld(t)
	integrationPR(t, w)
	w.forge("state", "MERGED")
	w.forge("base", "main")
	gittest.Run(t, w.origin, "branch", "-D", "w2")
	res, err := w.landPR9()
	if err != nil || res.Verdict != GateAlreadyMerged {
		t.Fatalf("a merged PR must recover, not refuse stale: %+v %v", res, err)
	}
	if !slices.Equal(res.IntegrationMembers, []string{"5", "6"}) {
		t.Errorf("member bookkeeping skipped: %v", res.IntegrationMembers)
	}
	reg, _ := LoadRegistry(w.dir)
	if rec, _ := reg.IntegrationByBranch(intBranch); !rec.Landed {
		t.Errorf("record not landed: %+v", rec)
	}
}

// Landed is persisted before the caller's cleanup: until the caller settles the
// record, the next gate of the PR replays the member bookkeeping.
func TestWorkerGateIntegrationReplaysUnsettledCleanup(t *testing.T) {
	w := integrationWorld(t)
	integrationPR(t, w)
	if res, err := w.landPR9(); err != nil || res.Verdict != GatePass {
		t.Fatalf("landing: %+v %v", res, err)
	}
	res, err := w.landPR9()
	if err != nil || res.Verdict != GateAlreadyMerged || !slices.Equal(res.IntegrationMembers, []string{"5", "6"}) {
		t.Fatalf("an unsettled landing must replay its members: %+v %v", res, err)
	}
	if err := SettleIntegration(w.dir, intBranch); err != nil {
		t.Fatal(err)
	}
	res, err = w.landPR9()
	if err != nil || len(res.IntegrationMembers) != 0 {
		t.Fatalf("a settled landing replays nothing: %+v %v", res, err)
	}
}
