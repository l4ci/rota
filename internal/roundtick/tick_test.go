package roundtick

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type fake struct {
	slots    []Slot
	queued   []string
	repaired []string
	drift    []string
	cands    []Candidate
	merged   [][]string
	assigned []string
	audit    []string
	mergeOut func(targets []string) []Merged
	assignFn func(id string) (string, error)
}

func (f *fake) env() Env {
	return Env{
		Slots:  func() []Slot { return f.slots },
		Queued: func() []string { return f.queued },
		Reconcile: func(context.Context) ([]string, []string, error) {
			return f.repaired, f.drift, nil
		},
		Merge: func(_ context.Context, t []string) ([]Merged, error) {
			f.merged = append(f.merged, t)
			if f.mergeOut != nil {
				return f.mergeOut(t), nil
			}
			var out []Merged
			for _, x := range t {
				out = append(out, Merged{Target: x, Landed: true, Detail: "landed"})
			}
			return out, nil
		},
		Candidates: func(context.Context) ([]Candidate, error) { return f.cands, nil },
		Assign: func(_ context.Context, id string) (string, error) {
			if f.assignFn != nil {
				return f.assignFn(id)
			}
			f.assigned = append(f.assigned, id)
			return "ben", nil
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
	if len(f.merged) != 1 || len(f.merged[0]) != 1 {
		t.Fatalf("merges %v, want one target", f.merged)
	}
	if len(f.assigned) != 1 {
		t.Fatalf("assigned %v, want 1", f.assigned)
	}
}

func TestMergesDoneSlotsAndQueuedAsOneBatch(t *testing.T) {
	f := &fake{
		slots:  []Slot{{Name: "ben", State: "done", Issue: "#1", PR: "#10"}, {Name: "dana", State: "done", Issue: "#2"}},
		queued: []string{"#11"},
	}
	r, err := Run(context.Background(), f.env())
	if err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{"ben", "#11"}}; !reflect.DeepEqual(f.merged, want) {
		t.Fatalf("merged %v, want %v (done with no PR is not mergeable)", f.merged, want)
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
	if len(f.merged) != 0 || len(r.NeedsYou) != 2 {
		t.Fatalf("merged %v needsYou %+v", f.merged, r.NeedsYou)
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
	if len(f.assigned) != 0 || len(f.merged) != 0 {
		t.Fatal("an attention state must not be answered, merged or reassigned")
	}
}

func TestFailedGateIsEscalatedAndVerifyFailureHeld(t *testing.T) {
	f := &fake{slots: []Slot{{Name: "ben", State: "done", PR: "#10"}, {Name: "dana", State: "done", PR: "#11"}}}
	f.mergeOut = func(t []string) []Merged {
		return []Merged{
			{Target: "ben", Verdict: "verify-failed", Hold: true, Detail: "tests red"},
			{Target: "dana", Verdict: "stale", Detail: "behind main"},
		}
	}
	r, err := Run(context.Background(), f.env())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.NeedsYou) != 2 || r.Held["ben"] == "" || r.Held["dana"] != "" {
		t.Fatalf("needsYou %+v held %v", r.NeedsYou, r.Held)
	}
	// The next tick does not re-run a held gate, and keeps escalating it.
	f.merged = nil
	e := f.env()
	e.Held = r.Held
	r2, err := Run(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{"dana"}}; !reflect.DeepEqual(f.merged, want) {
		t.Fatalf("second tick gated %v, want only dana", f.merged)
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
	f.assignFn = func(id string) (string, error) {
		if id == "#1" {
			return "", &Refusal{Why: "overlap"}
		}
		f.assigned = append(f.assigned, id)
		return "ben", nil
	}
	r, err := Run(context.Background(), f.env())
	if err != nil || !reflect.DeepEqual(f.assigned, []string{"#2"}) || len(r.NeedsYou) != 0 {
		t.Fatalf("%v %v %+v", err, f.assigned, r)
	}
}

func TestAssignFailureStopsTheTick(t *testing.T) {
	f := &fake{slots: []Slot{{Name: "ben", State: "idle"}}, cands: []Candidate{{"#1", true}}}
	f.assignFn = func(string) (string, error) { return "", errors.New("forge down") }
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
	f.assignFn = func(id string) (string, error) { return "", &Refusal{Why: "no"} }
	e := f.env()
	e.Review = func(context.Context) ([]string, error) { return []string{"#9"}, nil }
	r, err := Run(context.Background(), e)
	if err != nil || len(r.Did) != 1 || r.Did[0].Action != "mint" {
		t.Fatalf("%+v %v", r, err)
	}
}
