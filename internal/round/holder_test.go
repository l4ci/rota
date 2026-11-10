package round

import (
	"testing"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/worker"
)

// The holder pid reaches every verb's lease check through Env.HolderPID alone:
// a verb whose opts leave the pid unset still sees the holder (100) the env
// names, and one that names another pid (999) is refused.

func TestPickTakesTheHolderPIDFromTheEnv(t *testing.T) {
	f := newPickFx(t)
	f.bothHavePRs(t)
	f.env.HolderPID = 999
	_, err := f.env.Pick(bg, f.root, f.be, PickOpts{ID: "12", PR: "#7", Reason: "x"})
	wantBlocked(t, err, BlockNoRound)
	f.env.HolderPID = 100
	if _, err := f.env.Pick(bg, f.root, f.be, PickOpts{ID: "12", PR: "#7", Reason: "x"}); err != nil {
		t.Fatalf("env holder: %v", err)
	}
}

func TestReturnTakesTheHolderPIDFromTheEnv(t *testing.T) {
	f := newMoveFx(t)
	f.env.HolderPID = 999
	_, err := f.ret("ben", "x", func(o *ReturnOpts) { o.InSlot = false })
	if blockedBy(t, err) != BlockNotYourSlot {
		t.Fatalf("not the holder: %v", err)
	}
	f.env.HolderPID = 100
	if _, err := f.ret("ben", "x", func(o *ReturnOpts) { o.InSlot = false }); err != nil {
		t.Fatalf("env holder: %v", err)
	}
}

func TestReclaimTakesTheHolderPIDFromTheEnv(t *testing.T) {
	f := newMoveFx(t)
	f.env.HolderPID = 999
	_, err := f.env.Reclaim(bg, f.root, f.be, ReclaimOpts{Slot: "ben"})
	if blockedBy(t, err) != BlockNoRound {
		t.Fatalf("not the holder: %v", err)
	}
	f.env.HolderPID = 100
	_, err = f.env.Reclaim(bg, f.root, f.be, ReclaimOpts{Slot: "ben"})
	if err != nil && blockedBy(t, err) == BlockNoRound {
		t.Fatalf("env holder refused as no-round: %v", err)
	}
}

func TestWindDownTakesTheHolderPIDFromTheEnv(t *testing.T) {
	f := newAssignFixture(t)
	f.env.HolderPID = 999
	if _, err := f.env.WindDown(bg, f.root, f.be, WindDownOpts{Settings: f.set}); err == nil {
		t.Fatal("not the holder must be refused")
	}
	f.verifyWith(t, `["true"]`)
	f.env.HolderPID = 100
	if _, err := f.env.WindDown(bg, f.root, f.be, WindDownOpts{Settings: f.set}); err != nil {
		t.Fatalf("env holder: %v", err)
	}
}

func TestStartTakesTheHolderPIDFromTheEnv(t *testing.T) {
	root := newRepo(t, nil)
	e := Env{Git: git.Exec, Base: "main", Worker: wk(worker.Env{Getenv: noEnv, Config: diskConfig}), Lease: fakeLease("h", 100, 200), HolderPID: 100}
	st, err := e.Start(bg, root, StartOpts{Scope: roundcfg.ScopeMilestone, Settings: startOpts(roundcfg.ScopeMilestone).Settings, DefaultNum: 2, Dispatch: "tmux"})
	if err != nil {
		t.Fatal(err)
	}
	if st.Lease.PID != 100 {
		t.Fatalf("lease bound to pid %d, want the env's 100", st.Lease.PID)
	}
}
