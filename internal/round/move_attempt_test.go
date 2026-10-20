package round

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/worker"
)

// stamped commits a file in slot's worktree carrying the attempt trailer and
// pushes it, as a worker does.
func (f *moveFx) stamped(t *testing.T, slot, file, attempt string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.wt(slot), file), []byte(slot), 0o644); err != nil {
		t.Fatal(err)
	}
	sh(t, f.wt(slot), "add", file)
	sh(t, f.wt(slot), "commit", "-q", "-m", "work on "+file+"\n\n"+worker.AttemptTrailer+": "+attempt)
	sh(t, f.wt(slot), "push", "-q", "origin", "HEAD")
}

// queueBensPR moves ben's issue-12 PR onto the review queue the way assign
// does: ben holds a recorded PR and is given another issue.
func (f *moveFx) queueBensPR(t *testing.T) {
	t.Helper()
	if _, err := worker.UpdateSlot(f.root, "ben", func(s *worker.Slot) {
		s.SetPR("https://github.com/o/r/pull/7")
		s.MarkState("done", "")
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.assign("13", "ben"); err != nil {
		t.Fatal(err)
	}
	if worker.LoadRegistryTolerant(f.root).QueuedIssue("12") == nil {
		t.Fatal("ben's PR is not queued")
	}
}

// A transfer records the branch tip the receiver inherits under a new attempt,
// so the gate does not judge the sender's commits against it, and a failed
// dispatch or a queued PR does not change that.
func TestTransferRecordsTheInheritedTipAndTheGateKeepsTheReceiversPR(t *testing.T) {
	f := newMoveFx(t)
	benAttempt := f.slot("ben").Attempt()
	f.stamped(t, "ben", "stamped.txt", benAttempt)
	f.queueBensPR(t)
	f.env.Worker.NewHost = func(host.Kind) host.Host { return &failingHost{hostFake: f.host} }
	if _, err := f.transfer("12", "dana", nil); err == nil {
		t.Fatal("the dispatch must fail")
	}
	dana := f.slot("dana")
	tip := gitIn(t, f.wt("dana"), "rev-parse", "HEAD")
	if dana.Attempt() == "" || dana.Attempt() == benAttempt || dana.AttemptBase() != tip {
		t.Fatalf("dana attempt %q base %q, want a new attempt and base %s", dana.Attempt(), dana.AttemptBase(), tip)
	}
	gate := func() worker.GateResult {
		res, _ := f.env.Worker.Gate(bg, f.root, worker.GateOpts{Slot: "dana", Base: "main", CheckOnly: true})
		return res
	}
	if res := gate(); res.Verdict == worker.GateStaleAttempt {
		t.Fatalf("dispatch half done, the receiver's PR is refused: %+v", res)
	}
	f.env.Worker.NewHost = func(host.Kind) host.Host { return &killHost{hostFake: f.host, killed: &f.killed} }
	if _, err := f.transfer("12", "dana", nil); err != nil {
		t.Fatal(err)
	}
	if d := f.slot("dana"); d.Attempt() != dana.Attempt() || d.AttemptBase() != tip {
		t.Errorf("the resume keeps the attempt and its base: %q %q", d.Attempt(), d.AttemptBase())
	}
	if res := gate(); res.Verdict == worker.GateStaleAttempt {
		t.Fatalf("after the transfer the receiver's PR is refused: %+v", res)
	}
	f.stamped(t, "dana", "own.txt", f.slot("dana").Attempt())
	if res := gate(); res.Verdict == worker.GateStaleAttempt {
		t.Fatalf("the receiver's own commit is refused: %+v", res)
	}
	f.stamped(t, "dana", "late.txt", benAttempt)
	if res := gate(); res.Verdict != worker.GateStaleAttempt || !strings.Contains(res.Err, benAttempt) {
		t.Fatalf("a commit from the sender's attempt after the transfer must be refused: %+v", res)
	}
}

// An issue handed back and assigned afresh finds the earlier attempt's work on
// its branch: the new attempt records that tip, and a fresh branch records none
// of its own commits.
func TestAssignRecordsTheTipOfWorkAlreadyOnTheBranch(t *testing.T) {
	f := newMoveFx(t)
	if _, err := f.ret("ben", "handing back", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.assign("12", "dana"); err != nil {
		t.Fatal(err)
	}
	dana := f.slot("dana")
	tip := gitIn(t, f.wt("dana"), "rev-parse", "HEAD")
	if dana.Attempt() == "" || dana.AttemptBase() != tip {
		t.Fatalf("attempt %q base %q, want base %s", dana.Attempt(), dana.AttemptBase(), tip)
	}
}
