package round

import (
	"errors"
	"reflect"
	"testing"
)

// trace records the order steps ran and were undone in.
func traced(log *[]string, name string, err error) step {
	return step{
		name: name,
		do:   func() error { *log = append(*log, "do "+name); return err },
		undo: func() { *log = append(*log, "undo "+name) },
	}
}

func TestRunStepsRunsInOrder(t *testing.T) {
	var log []string
	if err := runSteps([]step{traced(&log, "a", nil), traced(&log, "b", nil)}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"do a", "do b"}; !reflect.DeepEqual(log, want) {
		t.Errorf("got %v, want %v", log, want)
	}
}

func TestRunStepsUndoesEarlierStepsLatestFirst(t *testing.T) {
	var log []string
	boom := errors.New("boom")
	err := runSteps([]step{traced(&log, "a", nil), traced(&log, "b", nil), traced(&log, "c", boom), traced(&log, "d", nil)})
	if !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
	// The failed step is not undone and later steps never ran.
	if want := []string{"do a", "do b", "do c", "undo b", "undo a"}; !reflect.DeepEqual(log, want) {
		t.Errorf("got %v, want %v", log, want)
	}
}

func TestRunStepsKeepLeavesEarlierStepsInPlace(t *testing.T) {
	var log []string
	boom := errors.New("boom")
	last := traced(&log, "dispatch", boom)
	last.keep = true
	if err := runSteps([]step{traced(&log, "a", nil), last}); !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
	if want := []string{"do a", "do dispatch"}; !reflect.DeepEqual(log, want) {
		t.Errorf("got %v, want %v", log, want)
	}
}

func TestRunStepsSkippedStepIsStillUndone(t *testing.T) {
	var log []string
	a := traced(&log, "a", nil)
	a.skip = func() bool { return true } // done by an earlier call
	if err := runSteps([]step{a, traced(&log, "b", errors.New("boom"))}); err == nil {
		t.Fatal("want the failure")
	}
	if want := []string{"do b", "undo a"}; !reflect.DeepEqual(log, want) {
		t.Errorf("a resumed call that fails rolls back what the first left: got %v, want %v", log, want)
	}
}

func TestClaimStepUndoReleasesAndUnbinds(t *testing.T) {
	be := &fakeRemote{}
	unbound := false
	s := claimStep(be, "12", "ben@1", 1, func() { unbound = true })
	if err := s.do(); err != nil || be.claims["12"] != "ben@1" {
		t.Fatalf("claim: %v %v", err, be.claims)
	}
	s.undo()
	if len(be.claims) != 0 || !unbound {
		t.Errorf("undo must release the claim and unbind: %v %v", be.claims, unbound)
	}
}

func TestClaimStepLostClaimIsBlockedAndUndoesNothingElse(t *testing.T) {
	be := &fakeRemote{claimedBy: "dana@1"}
	steps := []step{claimStep(be, "12", "ben@1", 1, func() { t.Error("unbind without a claim") })}
	err := runSteps(steps)
	if blockedBy(t, err) != BlockClaimed {
		t.Fatalf("got %v", err)
	}
}

func TestStateStepUndoClearsStateAndChanged(t *testing.T) {
	be := &fakeRemote{}
	changed := false
	s := stateStep(be, "12", "ben@1", false, &changed)
	if err := s.do(); err != nil || be.bstates["12"] != "in-progress" || !changed {
		t.Fatalf("state: %v %v %v", err, be.bstates, changed)
	}
	s.undo()
	if len(be.bstates) != 0 || changed {
		t.Errorf("undo must clear the state and Changed: %v %v", be.bstates, changed)
	}
}

// failingStateBoard writes the state, then reports the write as failed: a
// partial label update.
type failingStateBoard struct {
	*fakeRemote
}

func (b *failingStateBoard) SetState(ref, state string) (bool, error) {
	b.fakeRemote.SetState(ref, state)
	if state == "in-progress" {
		return false, errors.New("label write failed")
	}
	return true, nil
}

func TestStateStepFailureClearsItsOwnPartialWrite(t *testing.T) {
	be := &failingStateBoard{&fakeRemote{}}
	changed := false
	if err := runSteps([]step{stateStep(be, "12", "ben@1", false, &changed)}); err == nil {
		t.Fatal("want the failure")
	}
	if len(be.bstates) != 0 || changed {
		t.Errorf("a failed state step must clear its partial write: %v %v", be.bstates, changed)
	}
}
