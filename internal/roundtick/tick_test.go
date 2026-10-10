package roundtick

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/worker"
)

type fake struct {
	slots    []Slot
	queued   []string
	repaired []string
	drift    []string
	cands    []Candidate
	gated    []string
	trained  [][]string
	assigned []string
	audit    []string
	gateOut  func(target string) GateOutcome
	trainOut func(targets []string) TrainOutcome
	baseOf   func(target string) string
	assignFn func(id string) ([]string, error)
}

func (f *fake) env() Env {
	return Env{
		Slots:  func() []Slot { return f.slots },
		Queued: func() []string { return f.queued },
		Reconcile: func(context.Context) ([]string, []string, error) {
			return f.repaired, f.drift, nil
		},
		BaseOf: func(t string) string {
			if f.baseOf != nil {
				return f.baseOf(t)
			}
			return "main"
		},
		Gate: func(_ context.Context, t, base string) GateOutcome {
			f.gated = append(f.gated, base+":"+t)
			if f.gateOut != nil {
				return f.gateOut(t)
			}
			return GateOutcome{Landed: true}
		},
		Train: func(_ context.Context, t []string, base string) TrainOutcome {
			f.trained = append(f.trained, append([]string{base}, t...))
			if f.trainOut != nil {
				return f.trainOut(t)
			}
			return TrainOutcome{Done: true}
		},
		Candidates: func(context.Context) ([]Candidate, error) { return f.cands, nil },
		Assign: func(_ context.Context, id string) ([]string, error) {
			if f.assignFn != nil {
				return f.assignFn(id)
			}
			f.assigned = append(f.assigned, id)
			return []string{"ben"}, nil
		},
		Audit: func(a Action) { f.audit = append(f.audit, a.Action+" "+a.Target) },
	}
}

func TestAssignsReadyToIdleSlots(t *testing.T) {
	f := &fake{
		slots: []Slot{{Name: "ben", State: "idle"}, {Name: "dana", State: "idle"}, {Name: "nia", State: "working", Issue: "#3"}},
		cands: []Candidate{{"#1", false}, {"#2", true}, {"#4", true}, {"#5", true}},
	}
	r, err := Run(context.Background(), f.env())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"#2", "#4"}; !reflect.DeepEqual(f.assigned, want) {
		t.Fatalf("assigned %v, want %v (two idle slots, unready skipped)", f.assigned, want)
	}
	if len(r.NeedsYou) != 0 || len(f.audit) != 2 {
		t.Fatalf("%+v audit %v", r, f.audit)
	}
}

func TestCapLimitsAssignsAndMerges(t *testing.T) {
	f := &fake{
		slots: []Slot{{Name: "a", State: "idle"}, {Name: "b", State: "idle"}, {Name: "c", State: "done", PR: "#1"}, {Name: "d", State: "done", PR: "#2"}},
		cands: []Candidate{{"#1", true}, {"#2", true}, {"#3", true}},
	}
	e := f.env()
	e.Cap = 1
	if _, err := Run(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if want := []string{"main:c"}; !reflect.DeepEqual(f.gated, want) || len(f.trained) != 0 {
		t.Fatalf("gated %v trained %v, want one gate", f.gated, f.trained)
	}
	if len(f.assigned) != 1 {
		t.Fatalf("assigned %v, want 1", f.assigned)
	}
}

func TestMergesDoneSlotsAndQueuedAsOneTrain(t *testing.T) {
	f := &fake{
		slots:  []Slot{{Name: "ben", State: "done", Issue: "#1", PR: "#10"}, {Name: "dana", State: "done", Issue: "#2"}},
		queued: []string{"#11"},
	}
	r, err := Run(context.Background(), f.env())
	if err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{"main", "ben", "#11"}}; !reflect.DeepEqual(f.trained, want) || len(f.gated) != 0 {
		t.Fatalf("trained %v gated %v, want %v (done with no PR is not mergeable)", f.trained, f.gated, want)
	}
	if len(r.Did) != 2 {
		t.Fatalf("%+v", r.Did)
	}
}

func TestHumanMergePolicyMergesNothing(t *testing.T) {
	f := &fake{slots: []Slot{{Name: "ben", State: "done", PR: "#10"}}, queued: []string{"#11"}}
	e := f.env()
	e.HumanMerge = true
	r, err := Run(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.gated)+len(f.trained) != 0 || len(r.NeedsYou) != 2 {
		t.Fatalf("gated %v trained %v needsYou %+v", f.gated, f.trained, r.NeedsYou)
	}
}

func TestEscalatesAttentionStatesAndDriftWithoutActing(t *testing.T) {
	f := &fake{
		slots: []Slot{
			{Name: "ben", State: "blocked", Issue: "#1"}, {Name: "dana", State: "needs-permission"},
			{Name: "nia", State: "limited"}, {Name: "kit", State: "dead"}},
		drift: []string{"stalled kit"},
	}
	r, err := Run(context.Background(), f.env())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.NeedsYou) != 5 {
		t.Fatalf("needsYou %+v", r.NeedsYou)
	}
	if len(f.assigned) != 0 || len(f.gated)+len(f.trained) != 0 {
		t.Fatal("an attention state must not be answered, merged or reassigned")
	}
}

func TestFailedGateIsEscalatedAndVerifyFailureHeld(t *testing.T) {
	f := &fake{slots: []Slot{{Name: "ben", State: "done", PR: "#10"}, {Name: "dana", State: "done", PR: "#11"}}}
	f.baseOf = func(t string) string { return "base-" + t } // two gates, not a train
	f.gateOut = func(t string) GateOutcome {
		if t == "ben" {
			return GateOutcome{Verdict: "verify-failed", Detail: "tests red"}
		}
		return GateOutcome{Verdict: "stale", Detail: "behind main"}
	}
	r, err := Run(context.Background(), f.env())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.NeedsYou) != 2 || r.Held["ben"] == "" || r.Held["dana"] != "" {
		t.Fatalf("needsYou %+v held %v", r.NeedsYou, r.Held)
	}
	// The next tick does not re-run a held gate, and keeps escalating it.
	f.gated = nil
	e := f.env()
	e.Held = r.Held
	r2, err := Run(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"base-dana:dana"}; !reflect.DeepEqual(f.gated, want) {
		t.Fatalf("second tick gated %v, want only dana", f.gated)
	}
	if r2.Held["ben"] == "" {
		t.Fatal("hold dropped while the slot is still done")
	}
	// Once the slot leaves done, the hold is forgotten.
	f.slots = []Slot{{Name: "ben", State: "working"}}
	e.Held = r2.Held
	r3, _ := Run(context.Background(), e)
	if len(r3.Held) != 0 {
		t.Fatalf("held %v after the slot moved on", r3.Held)
	}
}

func TestNewReportsEachItemOnce(t *testing.T) {
	f := &fake{slots: []Slot{{Name: "ben", State: "blocked", Issue: "#1"}}}
	r1, _ := Run(context.Background(), f.env())
	if len(r1.New) != 1 {
		t.Fatalf("first tick New %+v", r1.New)
	}
	e := f.env()
	e.Reported = r1.Reported
	r2, _ := Run(context.Background(), e)
	if len(r2.NeedsYou) != 1 || len(r2.New) != 0 {
		t.Fatalf("second tick must still list it but not wake: %+v", r2)
	}
}

func TestAssignRefusalSkipsToNextCandidate(t *testing.T) {
	f := &fake{slots: []Slot{{Name: "ben", State: "idle"}}, cands: []Candidate{{"#1", true}, {"#2", true}}}
	f.assignFn = func(id string) ([]string, error) {
		if id == "#1" {
			return nil, &round.BlockedError{By: round.BlockNotReady, Msg: "overlap"}
		}
		f.assigned = append(f.assigned, id)
		return []string{"ben"}, nil
	}
	r, err := Run(context.Background(), f.env())
	if err != nil || !reflect.DeepEqual(f.assigned, []string{"#2"}) || len(r.NeedsYou) != 0 {
		t.Fatalf("%v %v %+v", err, f.assigned, r)
	}
}

func TestAssignFailureStopsTheTick(t *testing.T) {
	f := &fake{slots: []Slot{{Name: "ben", State: "idle"}}, cands: []Candidate{{"#1", true}}}
	f.assignFn = func(string) ([]string, error) { return nil, errors.New("forge down") }
	if _, err := Run(context.Background(), f.env()); err == nil {
		t.Fatal("an unexpected assign failure must surface")
	}
}

func TestReconcileRepairsAreAudited(t *testing.T) {
	f := &fake{repaired: []string{"pr-unrecorded ben"}}
	r, _ := Run(context.Background(), f.env())
	if len(r.Did) != 1 || f.audit[0] != "reconcile pr-unrecorded ben" {
		t.Fatalf("%+v %v", r.Did, f.audit)
	}
}

// The #53 trigger runs inside the tick. The hook stands in for both triggers
// (threshold and idle-with-nothing-assignable): the tick's part is to audit the
// mint and assign what it minted ahead of the backlog, within the cap.
func TestReviewMintsAreAuditedAndAssignedFirst(t *testing.T) {
	for _, trigger := range []string{"threshold", "queue-empty"} {
		t.Run(trigger, func(t *testing.T) {
			f := &fake{
				slots: []Slot{{Name: "ben", State: "idle"}, {Name: "dana", State: "idle"}, {Name: "nia", State: "idle"}},
				cands: []Candidate{{"#1", true}, {"#9", true}},
			}
			if trigger == "queue-empty" {
				f.cands = nil
			}
			e := f.env()
			e.Cap = 2
			e.Review = func(context.Context) ([]string, error) { return []string{"#9", "#10", "#11"}, nil }
			r, err := Run(context.Background(), e)
			if err != nil {
				t.Fatal(err)
			}
			if want := []string{"#9", "#10"}; !reflect.DeepEqual(f.assigned, want) {
				t.Fatalf("assigned %v, want %v (minted first, cap 2)", f.assigned, want)
			}
			want := []string{"mint #9", "mint #10", "mint #11", "assign #9", "assign #10"}
			if !reflect.DeepEqual(f.audit, want) || len(r.Did) != len(want) {
				t.Fatalf("audit %v, want %v", f.audit, want)
			}
		})
	}
}

func TestNoReviewHookOrNothingDueMintsNothing(t *testing.T) {
	f := &fake{slots: []Slot{{Name: "ben", State: "idle"}}, cands: []Candidate{{"#1", true}}}
	e := f.env()
	if _, err := Run(context.Background(), e); err != nil || !reflect.DeepEqual(f.audit, []string{"assign #1"}) {
		t.Fatalf("no hook: %v %v", f.audit, err)
	}
	f.audit, f.assigned = nil, nil
	e.Review = func(context.Context) ([]string, error) { return nil, nil }
	if _, err := Run(context.Background(), e); err != nil || !reflect.DeepEqual(f.audit, []string{"assign #1"}) {
		t.Fatalf("nothing due: %v %v", f.audit, err)
	}
}

func TestReviewFailureIsEscalatedAndTickGoesOn(t *testing.T) {
	f := &fake{slots: []Slot{{Name: "ben", State: "idle"}}, cands: []Candidate{{"#1", true}}}
	e := f.env()
	e.Review = func(context.Context) ([]string, error) { return nil, errors.New("tracker down\nmore") }
	r, err := Run(context.Background(), e)
	if err != nil || !reflect.DeepEqual(f.assigned, []string{"#1"}) {
		t.Fatalf("tick should go on: %v %v", f.assigned, err)
	}
	if len(r.NeedsYou) != 1 || r.NeedsYou[0].Kind != "architecture-review" || r.NeedsYou[0].Why != "tracker down" {
		t.Fatalf("needsYou %+v", r.NeedsYou)
	}
}

func TestReviewMintRefusedStaysForLaterTick(t *testing.T) {
	f := &fake{slots: []Slot{{Name: "ben", State: "idle"}}}
	f.assignFn = func(id string) ([]string, error) { return nil, &round.BlockedError{By: round.BlockNotReady, Msg: "no"} }
	e := f.env()
	e.Review = func(context.Context) ([]string, error) { return []string{"#9"}, nil }
	r, err := Run(context.Background(), e)
	if err != nil || len(r.Did) != 1 || r.Did[0].Action != "mint" {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestGroupsByBaseGateAloneTrainTogether(t *testing.T) {
	f := &fake{slots: []Slot{
		{Name: "a", State: "done", PR: "#1"}, {Name: "b", State: "done", PR: "#2"},
		{Name: "c", State: "done", PR: "#3"}, {Name: "d", State: "done", PR: "#4"}}}
	f.baseOf = func(t string) string {
		if t == "c" {
			return "cycle"
		}
		return "main"
	}
	e := f.env()
	e.Cap = 4
	if _, err := Run(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if want := []string{"cycle:c"}; !reflect.DeepEqual(f.gated, want) {
		t.Fatalf("gated %v, want %v", f.gated, want)
	}
	if want := [][]string{{"main", "a", "b", "d"}}; !reflect.DeepEqual(f.trained, want) {
		t.Fatalf("trained %v, want %v", f.trained, want)
	}
}

func TestTrainFailureLandsSkipsAndHolds(t *testing.T) {
	f := &fake{slots: []Slot{
		{Name: "a", State: "done", PR: "#1"}, {Name: "b", State: "done", PR: "#2"},
		{Name: "c", State: "done", PR: "#3"}}}
	f.trainOut = func([]string) TrainOutcome {
		return TrainOutcome{Landed: []string{"a"}, Culprit: "b", Verdict: "verify-failed", Detail: "red"}
	}
	r, err := Run(context.Background(), f.env())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Did) != 1 || r.Did[0].Target != "a" || r.Did[0].Detail != "train into main" {
		t.Fatalf("did %+v", r.Did)
	}
	// c is a skipped bystander: no escalation, no hold. b is the culprit.
	if len(r.NeedsYou) != 1 || r.NeedsYou[0].Target != "b" || r.Held["b"] == "" || r.Held["c"] != "" {
		t.Fatalf("needsYou %+v held %v", r.NeedsYou, r.Held)
	}
}

func TestTrainBaseMovedSkipsEveryone(t *testing.T) {
	f := &fake{slots: []Slot{{Name: "a", State: "done", PR: "#1"}, {Name: "b", State: "done", PR: "#2"}}}
	f.trainOut = func([]string) TrainOutcome { return TrainOutcome{Verdict: "base-moved"} }
	r, err := Run(context.Background(), f.env())
	if err != nil || len(r.Did) != 0 || len(r.NeedsYou) != 0 {
		t.Fatalf("%v %+v", err, r)
	}
}

func TestStaleAndProvenanceFailuresAreNotHeld(t *testing.T) {
	for _, v := range []string{worker.GateStale, worker.GateProvenanceFail, worker.GateBestOfUnpicked} {
		f := &fake{slots: []Slot{{Name: "a", State: "done", PR: "#1"}}}
		f.gateOut = func(string) GateOutcome { return GateOutcome{Verdict: v, Detail: "x"} }
		r, _ := Run(context.Background(), f.env())
		if len(r.NeedsYou) != 1 || len(r.Held) != 0 {
			t.Fatalf("%s: needsYou %+v held %v", v, r.NeedsYou, r.Held)
		}
	}
}

func TestGateWithoutVerdictIsAGateErrorAndHeld(t *testing.T) {
	f := &fake{slots: []Slot{{Name: "a", State: "done", PR: "#1"}}}
	f.gateOut = func(string) GateOutcome { return GateOutcome{Detail: "boom"} }
	r, _ := Run(context.Background(), f.env())
	if len(r.NeedsYou) != 1 || r.NeedsYou[0].Why != "gate-error: boom" || r.Held["a"] == "" {
		t.Fatalf("needsYou %+v held %v", r.NeedsYou, r.Held)
	}
}

func TestBestOfAssignNamesBothSlots(t *testing.T) {
	f := &fake{slots: []Slot{{Name: "ben", State: "idle"}, {Name: "dana", State: "idle"}}, cands: []Candidate{{"#1", true}}}
	f.assignFn = func(string) ([]string, error) { return []string{"ben", "dana"}, nil }
	r, err := Run(context.Background(), f.env())
	if err != nil || len(r.Did) != 1 || r.Did[0].Detail != "to ben and dana" {
		t.Fatalf("%v %+v", err, r.Did)
	}
}

func TestBlockedByNoRoundIsAFailureNotARefusal(t *testing.T) {
	f := &fake{slots: []Slot{{Name: "ben", State: "idle"}}, cands: []Candidate{{"#1", true}}}
	f.assignFn = func(string) ([]string, error) {
		return nil, &round.BlockedError{By: round.BlockNoRound, Msg: "no round"}
	}
	if _, err := Run(context.Background(), f.env()); err == nil {
		t.Fatal("no round must stop the tick")
	}
}

func TestHoldableSkipsAnUnpickedBestOf(t *testing.T) {
	if worker.ClassifyVerdict(worker.GateBestOfUnpicked).Hold || worker.ClassifyVerdict(worker.GateStale).Hold || !worker.ClassifyVerdict(worker.GateNotClosing).Hold {
		t.Error("only a verdict that needs a person is holdable")
	}
}

func TestCappedTickFillsNoSlotAndSaysWhy(t *testing.T) {
	f := &fake{
		slots: []Slot{{Name: "ben", State: "idle"}, {Name: "dana", State: "idle"}},
		cands: []Candidate{{"#1", true}, {"#2", true}},
	}
	why := "every work.accounts account is cooling down; slots fill again at 13:00 UTC"
	e := f.env()
	e.Capped = func(context.Context) string { return why }
	minted := 0
	e.Review = func(context.Context) ([]string, error) { minted++; return []string{"#9"}, nil }
	r, err := Run(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.assigned) != 0 || minted != 0 || r.Capped != why {
		t.Fatalf("assigned %v minted %d capped %q", f.assigned, minted, r.Capped)
	}
	// The cap lifts at the reset: the next tick fills the slots.
	e.Capped = func(context.Context) string { return "" }
	r, err = Run(context.Background(), e)
	if err != nil || len(f.assigned) != 2 || r.Capped != "" {
		t.Fatalf("after the reset: %v assigned %v capped %q", err, f.assigned, r.Capped)
	}
}

// A mixed config (Claude workers, Codex logins set) leaves the cap open, so the
// Claude pool running dry shows up as a quota refusal from Assign. The tick
// must say so, not report "nothing to do".
func TestQuotaRefusalIsReportedAsCapped(t *testing.T) {
	f := &fake{slots: []Slot{{Name: "ben", State: "idle"}}, cands: []Candidate{{"#1", true}, {"#2", true}}}
	why := "every work.accounts account is cooling down; slots fill again at 13:00 UTC"
	f.assignFn = func(string) ([]string, error) {
		return nil, &round.BlockedError{By: round.BlockQuota, Msg: why}
	}
	r, err := Run(context.Background(), f.env())
	if err != nil || r.Capped != why {
		t.Fatalf("err %v capped %q", err, r.Capped)
	}
	// A refusal for another reason stays quiet.
	f.assignFn = func(string) ([]string, error) {
		return nil, &round.BlockedError{By: round.BlockNotReady, Msg: "overlap"}
	}
	if r, err = Run(context.Background(), f.env()); err != nil || r.Capped != "" {
		t.Fatalf("err %v capped %q", err, r.Capped)
	}
}
