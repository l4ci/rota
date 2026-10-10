package round

import (
	"context"
	"errors"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/git"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/roundlease"
	"github.com/l4ci/rota/internal/worker"
)

func (f *assignFixture) windDown(mod func(*WindDownOpts)) (WoundDown, error) {
	o := WindDownOpts{HolderPID: 100, Settings: f.set}
	if mod != nil {
		mod(&o)
	}
	return f.env.WindDown(bg, f.root, f.be, o)
}

func (f *assignFixture) verifyWith(t *testing.T, cmds string) {
	t.Helper()
	cfg := `{"test":{"full":` + cmds + `}}`
	if err := os.WriteFile(filepath.Join(f.root, ".rota", "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
}

func outcomes(r WoundDown) map[string]string {
	m := map[string]string{}
	for _, s := range r.Slots {
		m[s.Name] = s.Outcome
	}
	return m
}

func TestWindDownParksReleasesAndSummarises(t *testing.T) {
	f := newAssignFixture(t)
	if _, err := f.assign("12", "ben", nil); err != nil {
		t.Fatal(err)
	}
	f.verifyWith(t, `["true"]`)
	res, err := f.windDown(nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != VerdictClean || !reflect.DeepEqual(res.Verified, []string{"true"}) || res.Lease != nil || res.Round != 1 {
		t.Fatalf("%+v", res)
	}
	if !reflect.DeepEqual(outcomes(res), map[string]string{"ben": OutcomeParked, "dana": OutcomeUnchanged}) {
		t.Fatalf("outcomes: %v", outcomes(res))
	}
	for _, s := range res.Slots {
		if s.Name == "ben" && s.Issue != "12" {
			t.Errorf("the summary keeps the issue the slot held: %+v", s)
		}
	}
	gres, _ := git.Exec(bg, filepath.Join(f.root, ".worktrees", "ben"), "symbolic-ref", "--short", "HEAD")
	out := gres.Stdout
	if got := out[:len(out)-1]; got != "park/ben" {
		t.Errorf("ben must be parked, on %q", got)
	}
	s := worker.LoadRegistryTolerant(f.root).Slot("ben")
	if s.Task() != "" || s.ClaimID() != "" || s.State() != "idle" {
		t.Errorf("a parked slot holds nothing: %v", s)
	}
	if _, held := f.be.claims["12"]; held {
		t.Error("the claim must be released")
	}
	if _, st, _ := f.env.ReadLease(bg, f.root); st != roundlease.None {
		t.Errorf("a clean wind-down releases the lease: %v", st)
	}
}

func TestWindDownClearsHandleSoReconcileSeesNoDeadTab(t *testing.T) {
	f := newAssignFixture(t)
	if _, err := f.assign("12", "ben", nil); err != nil {
		t.Fatal(err)
	}
	if worker.LoadRegistryTolerant(f.root).Slot("ben").Handle() == "" {
		t.Fatal("the fixture must dispatch ben into a tab")
	}
	f.verifyWith(t, `["true"]`)
	if _, err := f.windDown(nil); err != nil {
		t.Fatal(err)
	}
	if h := worker.LoadRegistryTolerant(f.root).Slot("ben").Handle(); h != "" {
		t.Errorf("a parked slot keeps no handle, got %q", h)
	}
	// The worker's tab closes after the round.
	f.env.Snapshot = func(context.Context) ([]host.Agent, error) { return nil, nil }
	rep, err := f.env.Status(bg, f.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, fd := range rep.Findings {
		if fd.Kind == DeadTab {
			t.Errorf("reconcile after wind-down reports a dead-tab: %+v", fd)
		}
	}
}

func TestWindDownVerifyFailureKeepsTheLeaseButParks(t *testing.T) {
	f := newAssignFixture(t)
	if _, err := f.assign("12", "ben", nil); err != nil {
		t.Fatal(err)
	}
	f.verifyWith(t, `["true","exit 3"]`)
	res, err := f.windDown(nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != VerdictVerifyFailed || res.Lease == nil || !reflect.DeepEqual(res.Verified, []string{"true"}) {
		t.Fatalf("%+v", res)
	}
	if outcomes(res)["ben"] != OutcomeParked {
		t.Errorf("slots are parked whatever the verify said: %v", outcomes(res))
	}
	if _, st, _ := f.env.ReadLease(bg, f.root); st != roundlease.Live {
		t.Errorf("a red base keeps the lease: %v", st)
	}
}

func TestWindDownRefusesWhileASlotHoldsWork(t *testing.T) {
	f := newAssignFixture(t)
	if _, err := f.assign("12", "ben", nil); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(f.root, ".worktrees", "ben")
	os.WriteFile(filepath.Join(wt, "wip.txt"), []byte("x"), 0o644)
	var killed []string
	f.env.Worker.NewHost = func(string) host.Host { return &killHost{hostFake: f.host, killed: &killed} }
	res, err := f.windDown(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(killed) != 0 || worker.LoadRegistryTolerant(f.root).Slot("ben").Handle() == "" {
		t.Errorf("a retained slot keeps its session and handle: killed %v", killed)
	}
	if res.Verdict != VerdictHoldsWork || !res.Retained || res.Lease == nil {
		t.Fatalf("%+v", res)
	}
	got := outcomes(res)
	if got["ben"] != OutcomeRetained || got["dana"] != OutcomeUnchanged {
		t.Fatalf("outcomes %v", got)
	}
	for _, s := range res.Slots {
		if s.Name == "ben" && len(s.Dirty) != 1 {
			t.Errorf("the dirty file is reported: %+v", s)
		}
	}
	if s := worker.LoadRegistryTolerant(f.root).Slot("ben"); s.Task() != "12" {
		t.Errorf("a retained slot keeps its task: %v", s)
	}
	if _, held := f.be.claims["12"]; !held {
		t.Error("a retained slot keeps its claim")
	}
	// Fix the slot and run again: now it completes.
	os.Remove(filepath.Join(wt, "wip.txt"))
	res, err = f.windDown(nil)
	if err != nil || res.Verdict != VerdictClean || outcomes(res)["ben"] != OutcomeParked {
		t.Fatalf("second run: %v %+v", err, res)
	}
}

func TestWindDownNeedsTheLeaseAndTheBase(t *testing.T) {
	f := newAssignFixture(t)
	var we *exitcode.Error
	if _, err := f.windDown(func(o *WindDownOpts) { o.HolderPID = 999 }); !errors.As(err, &we) || we.Exit != exitcode.ExitResolution {
		t.Fatalf("a process without the lease: %v", err)
	}
	f.verifyWith(t, `["true"]`)
	sh(t, f.root, "checkout", "-q", "-b", "side")
	if _, err := f.windDown(nil); !errors.As(err, &we) || we.Exit != exitcode.ExitResolution {
		t.Fatalf("the root must be on the base: %v", err)
	}
	if _, st, _ := f.env.ReadLease(bg, f.root); st != roundlease.Live {
		t.Errorf("a refusal keeps the lease: %v", st)
	}
	sh(t, f.root, "checkout", "-q", "main")
	// --no-verify needs neither commands nor the base.
	sh(t, f.root, "checkout", "-q", "side")
	res, err := f.windDown(func(o *WindDownOpts) { o.NoVerify = true })
	if err != nil || res.Verdict != VerdictClean || !res.VerifySkipped {
		t.Fatalf("no-verify: %v %+v", err, res)
	}
}

func TestWindDownWithoutVerifyCommandsSaysSo(t *testing.T) {
	f := newAssignFixture(t)
	res, err := f.windDown(nil)
	if err != nil || res.Verdict != VerdictClean || !res.VerifySkipped || len(res.Warnings) == 0 {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestWindDownEndsAParkedSlotsSession(t *testing.T) {
	f := newAssignFixture(t)
	if _, err := f.assign("12", "ben", nil); err != nil {
		t.Fatal(err)
	}
	var killed []string
	f.env.Worker.NewHost = func(string) host.Host { return &killHost{hostFake: f.host, killed: &killed} }
	f.verifyWith(t, `["true"]`)
	if _, err := f.windDown(nil); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(killed, []string{"ben"}) {
		t.Errorf("wind-down kills the parked slot's session, once, and skips slots with no handle: %v", killed)
	}
}

type deadKillHost struct{ *hostFake }

func (deadKillHost) Kill(context.Context, string, string) error {
	return errors.New("close not proved")
}

func TestWindDownKeepsHandleWhenTheKillIsNotProved(t *testing.T) {
	f := newAssignFixture(t)
	if _, err := f.assign("12", "ben", nil); err != nil {
		t.Fatal(err)
	}
	f.env.Worker.NewHost = func(string) host.Host { return deadKillHost{f.host} }
	f.verifyWith(t, `["true"]`)
	res, err := f.windDown(nil)
	if err != nil || res.Verdict != VerdictClean || outcomes(res)["ben"] != OutcomeParked {
		t.Fatalf("still parks: %v %+v", err, res)
	}
	if worker.LoadRegistryTolerant(f.root).Slot("ben").Handle() == "" {
		t.Error("an unproved kill keeps the handle")
	}
	found := false
	for _, w := range res.Warnings {
		found = found || strings.Contains(w, "SESSION-KEPT ben")
	}
	if !found {
		t.Errorf("warnings: %v", res.Warnings)
	}
}

// Wind-down shares the gate's runner: a cancelled verify returns while a
// grandchild still holds the output pipe.
func TestWindDownVerifyCancelReturnsWhileGrandchildHoldsPipe(t *testing.T) {
	f := newAssignFixture(t)
	f.verifyWith(t, `["sleep 30; sleep 30"]`)
	ctx, cancel := context.WithTimeout(bg, 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	res, err := f.env.WindDown(ctx, f.root, f.be, WindDownOpts{HolderPID: 100, Settings: f.set})
	if time.Since(start) > 10*time.Second {
		t.Fatalf("a cancelled wind-down verify hung for %v", time.Since(start))
	}
	if err == nil && res.Verdict != VerdictVerifyFailed {
		t.Errorf("verdict = %q, want %q", res.Verdict, VerdictVerifyFailed)
	}
}

// addExternal registers an adopted slot on a fresh branch and worktree.
func (f *assignFixture) addExternal(t *testing.T, name, branch, pr string) string {
	t.Helper()
	wt := filepath.Join(f.root, ".worktrees", name)
	sh(t, f.root, "worktree", "add", "-q", "-b", branch, wt, "main")
	if err := worker.RegisterExternal(f.root, name, branch, wt, "main", worker.IssueFromBranch(branch), pr); err != nil {
		t.Fatal(err)
	}
	return wt
}

func TestWindDownReleasesMergedExternalSlots(t *testing.T) {
	f := newAssignFixture(t)
	wt1 := f.addExternal(t, "ext-1", "codex/12-a", "https://github.com/o/r/pull/7")
	wt2 := f.addExternal(t, "ext-2", "codex/13-b", "https://github.com/o/r/pull/8")
	f.env.Forge = (&fakeRemote{states: map[int]string{7: "merged", 8: "open"}}).asForge()
	f.verifyWith(t, `["true"]`)
	res, err := f.windDown(nil)
	if err != nil {
		t.Fatal(err)
	}
	got := outcomes(res)
	if got["ext-1"] != OutcomeReleased || got["ext-2"] != OutcomeOpen {
		t.Fatalf("outcomes: %v", got)
	}
	reg := worker.LoadRegistryTolerant(f.root)
	if reg.Slot("ext-1") != nil || reg.Slot("ext-2") == nil {
		t.Errorf("registry after wind-down: ext-1=%v ext-2=%v", reg.Slot("ext-1"), reg.Slot("ext-2"))
	}
	for _, wt := range []string{wt1, wt2} {
		if _, err := os.Stat(wt); err != nil {
			t.Errorf("wind-down removed %s: %v", wt, err)
		}
	}
	if s := reg.Slot("ext-2"); s.Branch() != "codex/13-b" || s.Task() != "13" {
		t.Errorf("an open external slot must not be parked: %v", s)
	}
}

func TestWindDownLeavesExternalSlotsAloneWhenStatusIsUnreadable(t *testing.T) {
	f := newAssignFixture(t)
	f.addExternal(t, "ext-1", "codex/12-a", "https://github.com/o/r/pull/7")
	f.env.Forge = nil // PR state unreadable: nothing proves a merge
	f.verifyWith(t, `["true"]`)
	res, err := f.windDown(nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcomes(res)["ext-1"] == OutcomeReleased || worker.LoadRegistryTolerant(f.root).Slot("ext-1") == nil {
		t.Errorf("released without proof of a merge: %v", outcomes(res))
	}
}
