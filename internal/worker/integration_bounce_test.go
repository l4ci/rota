package worker

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/verdict"
)

// Regression tests for the review findings of round 55 on #3096.

func intRecord(t *testing.T, w *world) Integration {
	t.Helper()
	reg, err := LoadRegistry(w.dir)
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := reg.IntegrationByBranch(intBranch)
	if !ok {
		t.Fatal("no integration record")
	}
	return rec
}

func TestIntegrationPRBodyHasDecisionSections(t *testing.T) {
	body := IntegrationPRBody(Integration{Branch: intBranch, Round: 55, Base: "main",
		Members: []IntegrationMember{{Target: "7", PR: "p7", Issue: "5"}, {Target: "8", PR: "p8", Issue: "#6"}}})
	if got, _ := requiredPRSection(body); got != "" {
		t.Fatalf("the body rota writes is refused: %s\n%s", got, body)
	}
	if !strings.Contains(body, "Closes #5\n") || !strings.Contains(body, "Closes #6\n") {
		t.Fatalf("closing lines: %s", body)
	}
}

func TestTrainIntegrationNoVerifyRecordsNoTip(t *testing.T) {
	w := integrationWorld(t)
	w.setConfig(`{"test":{"full":[]}}`)
	w.forge("head#7", "w1")
	w.forge("sha#7", w.forgeWord("sha"))
	res, err := w.train(TrainOpts{Targets: []string{"7", "8"}, Integration: "a", NoVerify: true})
	if err != nil || res.Verdict != GatePass {
		t.Fatalf("no-verify train: %+v %v", res, err)
	}
	if rec := intRecord(t, w); rec.VerifiedTip != "" || rec.VerifiedBase != "" {
		t.Fatalf("a run that verified nothing recorded a verified tip: %+v", rec)
	}
	if !strings.Contains(strings.Join(res.Notes, "\n"), "INTEGRATION-UNVERIFIED") {
		t.Errorf("notes: %v", res.Notes)
	}
}

func TestWorkerGateIntegrationPendingRefusesWithoutRemote(t *testing.T) {
	w := integrationWorld(t)
	integrationPR(t, w)
	setPending := func(on bool) {
		if err := UpdateIntegration(w.dir, intBranch, func(it *Integration) {
			it.Members[1].Pending, it.Members[1].Conflicts = on, []string{"work2.txt"}
		}); err != nil {
			t.Fatal(err)
		}
	}
	setPending(true)
	gittest.Run(t, w.dir, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))
	res, err := w.landPR9()
	if err != nil || res.Verdict != GateStale || !strings.Contains(res.Err, "does not merge yet") {
		t.Fatalf("a pending member must refuse before any fetch: %+v %v", res, err)
	}
	setPending(false)
	res, err = w.landPR9()
	if err != nil || res.Verdict != GateCheckBroke || !strings.Contains(res.Err, "git fetch origin failed") {
		t.Fatalf("a failed fetch must refuse as check-broke: %+v %v", res, err)
	}
}

func TestWorkerGateIntegrationRefusesGoneMember(t *testing.T) {
	w := integrationWorld(t)
	integrationPR(t, w)
	gittest.Run(t, w.origin, "branch", "-D", "w2")
	res, err := w.landPR9()
	if err != nil || res.Verdict != GateStale || !strings.Contains(res.Err, "is gone from origin") {
		t.Fatalf("a member branch gone from the remote must refuse: %+v %v", res, err)
	}
	if w.onMain("work.txt") {
		t.Fatal("something landed")
	}
}

func TestTrainIntegrationPinsCheckedHead(t *testing.T) {
	w := integrationWorld(t)
	w.forge("head#7", "w1")
	w.forge("sha#7", w.forgeWord("sha"))
	checked := w.forgeWord("sha#8")
	pushed := false
	// ORDER lines are said after every member's check, before the train fetches.
	say := func(line string) {
		if pushed || !strings.HasPrefix(line, "ORDER ") {
			return
		}
		pushed = true
		gittest.Run(t, w.worker, "checkout", "-q", "w2")
		trainWrite2(t, w.worker, "late.txt")
		gittest.Run(t, w.worker, "push", "-q", "origin", "w2")
	}
	res, err := w.train(TrainOpts{Targets: []string{"7", "8"}, Integration: "a", Say: say})
	if err != nil || res.Verdict != GatePass || !pushed {
		t.Fatalf("train: %+v %v (pushed %v)", res, err, pushed)
	}
	if m, _ := intRecord(t, w).Member("8"); m.Head != checked {
		t.Fatalf("merged %s, not the checked head %s", m.Head, checked)
	}
	if err := exec.Command("git", "-C", w.origin, "cat-file", "-e", intBranch+":late.txt").Run(); err == nil {
		t.Fatal("a head pushed after the check was merged")
	}
	if !strings.Contains(strings.Join(res.Notes, "\n"), "PINNED 8") {
		t.Errorf("notes: %v", res.Notes)
	}
}

func TestWorkerGateIntegrationVerifiedBaseBindsMerge(t *testing.T) {
	w := integrationWorld(t)
	tip := integrationPR(t, w)
	res, err := w.env(false).Gate(bg, w.dir, GateOpts{Slot: "9", Base: "main", LandVerified: true, LandVerifiedTip: tip, LandVerifiedBase: strings.Repeat("e", 40)})
	if err != nil || res.Verdict != GateBaseMoved || !strings.Contains(res.Err, "main moved from") || w.onMain("work.txt") {
		t.Fatalf("a base other than the verified one landed: %+v %v", res, err)
	}
}

func TestTrainRefusesIntegrationPR(t *testing.T) {
	w := integrationWorld(t)
	integrationPR(t, w)
	for _, tg := range []string{"9", "https://github.com/o/r/pull/9"} {
		_, err := w.train(TrainOpts{Targets: []string{tg}})
		var ee *exitcode.Error
		if !errors.As(err, &ee) || ee.Exit != exitcode.ExitUsage || !strings.Contains(err.Error(), "rota worker gate") {
			t.Fatalf("a train of integration PR %s: %v", tg, err)
		}
	}
	if w.onMain("work.txt") {
		t.Fatal("something landed")
	}
}

func TestIntegrationFixReviewBlockedAtCoveringCommit(t *testing.T) {
	w := reviewWorld(t)
	integrationPR(t, w)
	fix := w.pushFix("fix.txt")
	w.record(intBranch, fix, verdict.ReviewSpec)
	// CONCERNS with a major finding: the bounce rule blocks it at its own
	// commit, though no FAIL blocks the branch outright.
	if _, err := verdict.AddBranch(w.dir, intBranch, verdict.Record{Kind: verdict.ReviewQuality, Verdict: verdict.Concerns, Sha: fix,
		Findings: []verdict.Finding{{Severity: "major", Title: "breaks the members"}}}); err != nil {
		t.Fatal(err)
	}
	// main moves and a rerun merges it, so the head is past the failing review.
	trainWrite(t, w, "later.txt", "later")
	gittest.Run(t, w.dir, "push", "-q", "origin", "main")
	if res, err := w.train(TrainOpts{Targets: []string{"7", "8"}, Integration: "a"}); err != nil || res.Verdict != GatePass {
		t.Fatalf("rerun: %+v %v", res, err)
	}
	w.pointPR9()
	if res, _ := w.gateCheck("9"); res.OK() {
		t.Fatalf("a failing review at the fix covered it once rota merged past it: %+v", res)
	}
}

func TestWorkerGateIntegrationRereadsRecordUnderLock(t *testing.T) {
	w := integrationWorld(t)
	integrationPR(t, w)
	e := w.env(false).WithDefaults()
	held, release := make(chan struct{}), make(chan struct{})
	lockDone := make(chan error, 1)
	go func() {
		lockDone <- e.WithIntegrationLock(context.Background(), w.dir, "a", func() error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	type out struct {
		res GateResult
		err error
	}
	gated := make(chan out, 1)
	// The gate resolves the git common dir twice before it blocks: for its
	// run key, then for the integration lock, after it read the record.
	ge := w.env(false).WithDefaults()
	inner, calls, locking := ge.Git, 0, make(chan struct{})
	var mu sync.Mutex
	ge.Git = func(ctx context.Context, dir string, args ...string) (git.Result, error) {
		if slices.Contains(args, "--git-common-dir") {
			mu.Lock()
			if calls++; calls == 2 {
				close(locking)
			}
			mu.Unlock()
		}
		return inner(ctx, dir, args...)
	}
	go func() {
		res, err := ge.Gate(bg, w.dir, GateOpts{Slot: "9", Base: "main"})
		gated <- out{res, err}
	}()
	<-locking
	// Only now, with the record read and the gate at the lock, a train
	// records member 8 as pending.
	if err := UpdateIntegration(w.dir, intBranch, func(it *Integration) {
		it.Members[1].Pending, it.Members[1].Conflicts = true, []string{"work2.txt"}
	}); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-lockDone; err != nil {
		t.Fatal(err)
	}
	g := <-gated
	if g.err != nil || g.res.Verdict != GateStale || !strings.Contains(g.res.Err, "does not merge yet") {
		t.Fatalf("the gate landed on the record it read before the lock: %+v %v", g.res, g.err)
	}
}

func TestRecordIntegrationKeepsLaterPRAndItems(t *testing.T) {
	w := integrationWorld(t)
	stale := Integration{Name: "a", Branch: intBranch, Round: 55, Base: "main"}
	if err := RecordIntegration(w.dir, stale); err != nil {
		t.Fatal(err)
	}
	if err := UpdateIntegration(w.dir, intBranch, func(it *Integration) {
		it.PR, it.Items = "https://github.com/o/r/pull/9", []string{"40"}
	}); err != nil {
		t.Fatal(err)
	}
	stale.Assembled = "abc"
	if err := RecordIntegration(w.dir, stale); err != nil {
		t.Fatal(err)
	}
	if rec := intRecord(t, w); rec.PR == "" || len(rec.Items) != 1 || rec.Assembled != "abc" {
		t.Fatalf("a train's write dropped what the CLI recorded: %+v", rec)
	}
}

func TestMemberIssueOfExternalPR(t *testing.T) {
	if got := memberIssue(Registry{}, TrainMember{Target: "12", PR: "https://github.com/o/r/pull/12", issue: "44"}); got != "44" {
		t.Fatalf("issue = %q, want the one the check gate resolved", got)
	}
}

func TestWorkerGateIntegrationAlreadyMergedEndsMembers(t *testing.T) {
	w := integrationWorld(t)
	integrationPR(t, w)
	w.forge("state", "MERGED")
	res, err := w.landPR9()
	if err != nil || res.Verdict != GateAlreadyMerged {
		t.Fatalf("retry after a forge merge: %+v %v", res, err)
	}
	if strings.Join(res.IntegrationMembers, ",") != "#5,#6" && strings.Join(res.IntegrationMembers, ",") != "5,6" {
		t.Fatalf("members not ended: %v", res.IntegrationMembers)
	}
	if !intRecord(t, w).Landed {
		t.Fatal("the record was not marked landed")
	}
}

func TestTrainIntegrationMergesReviewedHeadWhenRefMoves(t *testing.T) {
	w := integrationWorld(t)
	w.forge("head#7", "w1")
	w.forge("sha#7", w.forgeWord("sha"))
	reviewed := w.forgeWord("sha#8")
	// An unreviewed commit, fetched into the gate checkout.
	gittest.Run(t, w.worker, "checkout", "-q", "-B", "w2-late", "origin/w2")
	late := trainWrite2(t, w.worker, "late.txt")
	gittest.Run(t, w.worker, "push", "-q", "origin", "w2-late")
	gittest.Run(t, w.dir, "fetch", "-q", "origin", "w2-late")
	e := w.env(false).WithDefaults()
	inner, moved := e.Git, false
	e.Git = func(ctx context.Context, dir string, args ...string) (git.Result, error) {
		r, err := inner(ctx, dir, args...)
		// Right after member 8's check resolved origin/w2, another fetch moves it.
		if !moved && len(args) == 2 && args[0] == "rev-parse" && args[1] == "origin/w2" {
			moved = true
			if out, err := exec.Command("git", "-C", w.dir, "update-ref", "refs/remotes/origin/w2", late).CombinedOutput(); err != nil {
				t.Errorf("update-ref: %v %s", err, out)
			}
		}
		return r, err
	}
	res, err := e.Train(bg, w.dir, TrainOpts{Base: "main", Targets: []string{"7", "8"}, Integration: "a"})
	if !moved {
		t.Fatal("the member's head was never resolved as expected")
	}
	if err := exec.Command("git", "-C", w.origin, "cat-file", "-e", intBranch+":late.txt").Run(); err == nil {
		t.Fatalf("the unreviewed commit was merged: %+v", res)
	}
	if err == nil && res.Verdict == GatePass {
		if m, _ := intRecord(t, w).Member("8"); m.Head != reviewed {
			t.Fatalf("merged %s, not the reviewed %s", m.Head, reviewed)
		}
	}
}
