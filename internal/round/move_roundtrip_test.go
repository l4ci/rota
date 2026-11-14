package round

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// queueBen leaves issue 12 as a queued PR of ben whose worktree still has the
// branch checked out: the shape a slot freed without its worktree moving has.
func (f *moveFx) queueBen(t *testing.T) string {
	t.Helper()
	branch := f.slot("ben").Branch()
	gitIn(t, f.wt("ben"), "push", "-q", "origin", branch)
	if err := worker.Update(f.root, func(doc *worker.Doc) {
		doc.QueuePR(worker.QueuedPR{Issue: "12", Branch: branch, PR: "https://github.com/o/r/pull/7", Base: "main", From: "ben", ClaimID: "ben@1", Round: 1})
		if s := doc.Slot("ben"); s != nil {
			s.Park(false)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := freeSlot(f.root, "ben", false); err != nil {
		t.Fatal(err)
	}
	return branch
}

// #3322 AC-1: a slot whose held item's PR merged is not "holding" it, even with
// a dirty worktree: the park rules salvage the changes.
func TestTransferToASlotWhoseHeldPRMergedParksItEvenDirty(t *testing.T) {
	f := newMoveFx(t)
	if _, err := f.assign("13", "dana"); err != nil {
		t.Fatal(err)
	}
	f.commit(t, "dana", "dana-work.txt")
	worker.UpdateSlot(f.root, "dana", func(s *worker.Slot) { s.SetPR("https://github.com/o/r/pull/9") })
	f.forge.states = map[int]string{9: "merged"}
	worker.UpdateSlot(f.root, "dana", func(s *worker.Slot) { _ = s.MarkState("done", "") })
	if err := os.WriteFile(filepath.Join(f.wt("dana"), "local.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := f.transfer("12", "dana", nil)
	if err != nil {
		if strings.Contains(err.Error(), "holds 13") {
			t.Fatalf("a merged PR must not keep the slot busy: %v", err)
		}
		t.Fatal(err)
	}
	if got := f.slot("dana").Task(); got != "12" {
		t.Errorf("dana holds %q", got)
	}
}

// #3322 AC-1: a refusal for a dirty worktree names the dirty state, not a held item.
func TestTransferBusyMessageNamesTheDirtyState(t *testing.T) {
	msg := dirtyMsg("dana", "uncommitted changes")
	if strings.Contains(msg, "holds") || !strings.Contains(msg, "uncommitted changes") {
		t.Errorf("%q", msg)
	}
}

// #3322 AC-2: a queued PR's branch still checked out in the sender's worktree is
// detached there, and the receiver gets it.
func TestTransferDetachesTheBranchFromTheSendersWorktree(t *testing.T) {
	f := newMoveFx(t)
	branch := f.queueBen(t)
	if _, err := f.transfer("12", "dana", nil); err != nil {
		t.Fatal(err)
	}
	if cur := gitIn(t, f.wt("dana"), "symbolic-ref", "--short", "HEAD"); cur != branch {
		t.Errorf("dana is on %s", cur)
	}
	if out, _ := os.ReadFile(filepath.Join(f.wt("ben"), ".git")); len(out) == 0 {
		t.Fatal("ben's worktree vanished")
	}
	if res, _ := gitOut(t, f.wt("ben"), "symbolic-ref", "-q", "HEAD"); res != "" {
		t.Errorf("ben's worktree must be detached, is on %s", res)
	}
	if f.be.claims["12"] != "dana@1" {
		t.Errorf("claim %q", f.be.claims["12"])
	}
}

func gitOut(t *testing.T, dir string, args ...string) (string, int) {
	t.Helper()
	res, _ := git.Exec(bg, dir, args...)
	return strings.TrimSpace(res.Stdout), res.ExitCode
}

// #3322 AC-2 and AC-3: a branch checked out in a worktree that is neither the
// sender's nor the receiver's refuses before the claim, the record or the labels move.
func TestTransferRefusesWhenAnotherWorktreeHoldsTheBranch(t *testing.T) {
	f := newMoveFx(t)
	branch := f.queueBen(t)
	gitIn(t, f.wt("ben"), "switch", "-q", "--detach")
	other := filepath.Join(t.TempDir(), "other")
	gitIn(t, f.root, "worktree", "add", "-q", other, branch)
	_, err := f.transfer("12", "dana", nil)
	if err == nil {
		t.Fatal("transfer must refuse")
	}
	if blockedBy(t, err) != blockedby.SlotBusy || !strings.Contains(err.Error(), other) {
		t.Errorf("refusal: %v", err)
	}
	if f.be.claims["12"] != "ben@1" {
		t.Errorf("the claim must stay with the sender: %q", f.be.claims["12"])
	}
	if f.slot("dana").Task() != "" || worker.LoadRegistryTolerant(f.root).QueuedIssue("12") == nil {
		t.Error("nothing may move")
	}
}

// #3323 AC-1, AC-3, AC-4: to the human and back to a slot.
func TestTransferToHumanThenToASlotRoundTrips(t *testing.T) {
	f := newMoveFx(t)
	branch := f.slot("ben").Branch()
	f.forge.prs = []tracker.PR{{Number: 7, Branch: branch, URL: "https://github.com/o/r/pull/7", SameRepository: true}}
	if _, err := f.transfer("12", HumanTarget, nil); err != nil {
		t.Fatal(err)
	}
	if got := f.forge.labels[12]; len(got) != 1 {
		t.Fatalf("needs-human label: %v", got)
	}
	// AC-3: assign's hint names transfer, and transfer works.
	_, aerr := f.env.Assign(bg, f.root, f.be, AssignOpts{ID: "12", Agent: "dana", AcceptOpenPR: true})
	if aerr == nil || !strings.Contains(aerr.Error(), "transfer 12 --to dana") {
		t.Fatalf("assign hint: %v", aerr)
	}
	f.host.sent = ""
	res, err := f.transfer("12", "dana", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.From != HumanTarget || res.To != "dana" || res.ClaimID != "dana@1" || !res.Dispatched || res.Branch != branch {
		t.Fatalf("%+v", res)
	}
	if f.be.claims["12"] != "dana@1" || f.be.bstates["12"] != "in-progress" {
		t.Errorf("claim %v state %v", f.be.claims, f.be.bstates)
	}
	if got := f.forge.labels[12]; len(got) != 0 {
		t.Errorf("needs-human must be cleared: %v", got)
	}
	if cur := gitIn(t, f.wt("dana"), "symbolic-ref", "--short", "HEAD"); cur != branch {
		t.Errorf("dana is on %s", cur)
	}
	if !strings.Contains(f.host.sent, "You are dana") {
		t.Errorf("brief: %s", f.host.sent)
	}
}

// #3323 AC-2: neither slot-held, queued nor human-held still reads "no slot holds".
func TestTransferOfAnUnheldIssueStillSaysNoSlotHolds(t *testing.T) {
	f := newMoveFx(t)
	_, err := f.transfer("13", "dana", nil)
	if err == nil || !strings.Contains(err.Error(), "no slot holds 13") {
		t.Fatalf("%v", err)
	}
}
