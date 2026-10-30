package round

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
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
	gittest.Run(t, f.wt(slot), "add", file)
	gittest.Run(t, f.wt(slot), "commit", "-q", "-m", "work on "+file+"\n\n"+worker.AttemptTrailer+": "+attempt)
	gittest.Run(t, f.wt(slot), "push", "-q", "origin", "HEAD")
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
	f.wire(func(w *worker.Env) { w.NewHost = func(host.Kind) host.Host { return &failingHost{hostFake: f.host} } })
	if _, err := f.transfer("12", "dana", nil); err == nil {
		t.Fatal("the dispatch must fail")
	}
	dana := f.slot("dana")
	tip := gitIn(t, f.wt("dana"), "rev-parse", "HEAD")
	if dana.Attempt() == "" || dana.Attempt() == benAttempt || dana.AttemptBase() != tip {
		t.Fatalf("dana attempt %q base %q, want a new attempt and base %s", dana.Attempt(), dana.AttemptBase(), tip)
	}
	gate := func() worker.GateResult {
		res, _ := f.ws.Gate(bg, f.root, worker.GateOpts{Slot: "dana", Base: "main", CheckOnly: true})
		return res
	}
	if res := gate(); res.Verdict == worker.GateStaleAttempt {
		t.Fatalf("dispatch half done, the receiver's PR is refused: %+v", res)
	}
	f.wire(func(w *worker.Env) {
		w.NewHost = func(host.Kind) host.Host { return &killHost{hostFake: f.host, killed: &f.killed} }
	})
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
	benBranch := f.slot("ben").Branch()
	if _, err := f.ret("ben", "handing back", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.assign("12", "dana"); err != nil {
		t.Fatal(err)
	}
	dana := f.slot("dana")
	tip := gitIn(t, f.root, "rev-parse", "origin/"+benBranch)
	if dana.Attempt() == "" || dana.AttemptBase() != tip {
		t.Fatalf("attempt %q base %q, want the handed-back tip %s", dana.Attempt(), dana.AttemptBase(), tip)
	}
}

// A fresh assign cuts the branch itself: no commit on it is inherited.
func TestFreshAssignRecordsNoInheritedTip(t *testing.T) {
	f := newMoveFx(t)
	if _, err := f.assign("13", "dana"); err != nil {
		t.Fatal(err)
	}
	if s := f.slot("dana"); s.Attempt() == "" || s.AttemptBase() != "" {
		t.Fatalf("attempt %q base %q, want an attempt and no base", s.Attempt(), s.AttemptBase())
	}
}

// The sender pushed after root's last fetch: the receiver's base is that push,
// not the stale remote-tracking ref.
func TestTransferBaseIncludesAPushRootHasNotFetched(t *testing.T) {
	f := newMoveFx(t)
	branch := f.slot("ben").Branch()
	benAttempt := f.slot("ben").Attempt()
	f.queueBensPR(t)
	clone := filepath.Join(t.TempDir(), "other")
	gittest.Run(t, filepath.Dir(clone), "clone", "-q", f.origin, clone)
	gittest.Run(t, clone, "checkout", "-q", branch)
	os.WriteFile(filepath.Join(clone, "late.txt"), []byte("late"), 0o644)
	gittest.Run(t, clone, "add", "late.txt")
	gittest.Run(t, clone, "commit", "-q", "-m", "late push\n\n"+worker.AttemptTrailer+": "+benAttempt)
	gittest.Run(t, clone, "push", "-q", "origin", "HEAD")
	tip := gitIn(t, clone, "rev-parse", "HEAD")
	if _, err := f.transfer("12", "dana", nil); err != nil {
		t.Fatal(err)
	}
	if got := f.slot("dana").AttemptBase(); got != tip {
		t.Fatalf("base %s, want the pushed tip %s", got, tip)
	}
}

// A fresh-slot relay (round.freshAt) is a transfer: the new holder's PR is
// not refused for the sender's commits, but a later sender push is.
func TestFreshRelayKeepsTheReceiversPR(t *testing.T) {
	f, o := freshFx(t)
	benAttempt := f.slot("ben").Attempt()
	f.stamped(t, "ben", "stamped.txt", benAttempt)
	if got, err := f.env.ReviewRelay(bg, f.root, o); err != nil || got.Fresh != "dana" {
		t.Fatalf("%+v %v", got, err)
	}
	worker.UpdateSlot(f.root, "dana", func(s *worker.Slot) { s.SetPR("https://github.com/o/r/pull/9") })
	dana := f.slot("dana")
	if dana.AttemptBase() == "" || dana.Attempt() == benAttempt {
		t.Fatalf("attempt %q base %q", dana.Attempt(), dana.AttemptBase())
	}
	gate := func() worker.GateResult {
		res, _ := f.ws.Gate(bg, f.root, worker.GateOpts{Slot: "dana", Base: "main", CheckOnly: true})
		return res
	}
	if res := gate(); res.Verdict == worker.GateStaleAttempt {
		t.Fatalf("the fresh holder's PR is refused: %+v", res)
	}
	f.stamped(t, "dana", "late.txt", benAttempt)
	if res := gate(); res.Verdict != worker.GateStaleAttempt {
		t.Fatalf("a later sender push must be refused: %+v", res)
	}
}

// A branch name that git would read as an option never reaches git.
func TestBranchTipRefusesOptionLikeNames(t *testing.T) {
	f := newMoveFx(t)
	for _, b := range []string{"--upload-pack=touch pwned", "-x", "a b", "a:b", ""} {
		if got := f.env.branchTip(bg, f.root, b); got != "" {
			t.Errorf("branchTip(%q) = %q", b, got)
		}
	}
	if got := f.env.branchTip(bg, f.root, f.slot("ben").Branch()); got == "" {
		t.Error("a real branch has a tip")
	}
}

// The transfer brief is the receiver's only signed message from the
// orchestrator, so it lands in the receiver's relay log: a PR citing it as
// `orchestrator relay round N` passes the provenance check (#1299).
func TestTransferLogsTheBriefAsARelay(t *testing.T) {
	f := newMoveFx(t)
	if _, err := f.transfer("12", "dana", nil); err != nil {
		t.Fatal(err)
	}
	relays := f.slot("dana").Relays()
	if len(relays) != 1 {
		t.Fatalf("relays %d, want the transfer brief logged once", len(relays))
	}
	const cite = "## Approvals\n- take over the issue — orchestrator relay round 1\n"
	if got := worker.CheckApprovals(cite, relays); got != "" {
		t.Errorf("a PR citing the transfer is refused: %s", got)
	}
	if got := worker.CheckApprovals(strings.Replace(cite, "round 1", "round 2", 1), relays); got == "" {
		t.Error("a cite of a round never sent must still be refused")
	}
}

// A transfer that sends no brief logs no relay, so nothing can be cited: a
// failed dispatch, solo (the brief is returned, not sent) and --to human.
func TestTransferThatSendsNoBriefLogsNoRelay(t *testing.T) {
	for name, tc := range map[string]struct {
		to    string
		setup func(*moveFx)
		fails bool
	}{
		"failed dispatch": {"dana", func(f *moveFx) {
			f.wire(func(w *worker.Env) { w.NewHost = func(host.Kind) host.Host { return &failingHost{hostFake: f.host} } })
		}, true},
		"solo":     {"dana", func(f *moveFx) { setHost(t, f.root, host.Solo) }, false},
		"to human": {HumanTarget, func(*moveFx) {}, false},
	} {
		t.Run(name, func(t *testing.T) {
			f := newMoveFx(t)
			tc.setup(f)
			if _, err := f.transfer("12", tc.to, nil); (err != nil) != tc.fails {
				t.Fatalf("transfer error %v, want failure %v", err, tc.fails)
			}
			if got := f.slot("dana").Relays(); len(got) != 0 {
				t.Errorf("relays %v, want none", got)
			}
			const cite = "## Approvals\n- orchestrator relay round 1\n"
			if got := worker.CheckApprovals(cite, f.slot("dana").Relays()); got == "" {
				t.Error("a cite with no relay logged must be refused")
			}
		})
	}
}
