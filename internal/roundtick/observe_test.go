package roundtick

import (
	"context"
	"testing"

	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/worker"
)

// spy counts every call into an adapter that can change the world.
type spy struct{ calls []string }

func (s *spy) hit(name string) { s.calls = append(s.calls, name) }

// observed wires f for observation: every mutation adapter is a spy, and the
// read-only probes return the same facts the execution adapters would act on.
func observed(f *fake, s *spy, review *round.Architecture) Env {
	e := f.env()
	e.Reconcile = func(context.Context) ([]Finding, []Finding, error) { s.hit("reconcile"); return nil, nil, nil }
	e.RunGate = func(context.Context, string, string) GateRun { s.hit("gate"); return GateRun{} }
	e.RunTrain = func(context.Context, []string, string) TrainRun { s.hit("train"); return TrainRun{} }
	e.Assign = func(context.Context, string) ([]string, error) { s.hit("assign"); return nil, nil }
	e.Audit = func(Action) { s.hit("audit") }
	e.Say = func(string) { s.hit("say") }
	e.ReviewLoop = func(context.Context, Slot) ReviewOutcome { s.hit("review-loop"); return ReviewOutcome{} }
	e.AwaitingReview = func(context.Context, Slot, string) string { s.hit("awaiting-review"); return "" }
	e.Review = fakeReview{mint: func() ([]string, error) { s.hit("mint"); return nil, nil }}
	e.Observe = &ObservationSources{
		Reconcile: func(context.Context) (Inspection, error) {
			return Inspection{Proposed: f.repaired, Drift: f.drift}, nil
		},
		ReviewLoop: func(context.Context, Slot) ReviewOutcome { return ReviewOutcome{} },
		AwaitingReview: func(_ context.Context, sl Slot, _ string) string {
			if f.awaiting == nil {
				return ""
			}
			return f.awaiting(sl)
		},
	}
	if review != nil {
		a := *review
		e.Observe.ReviewFacts = func(context.Context) (round.Architecture, bool, error) { return a, false, nil }
	}
	return e
}

func TestObserveInvokesNoMutationAdapter(t *testing.T) {
	f := &fake{
		slots:    []Slot{{Name: "ben", State: "done", Issue: "#1", PR: "#10"}, {Name: "dana", State: "idle"}},
		queued:   []string{"#11"},
		repaired: []Finding{{"slot-stuck", "dana"}},
		cands:    []round.Candidate{cand("#2", true), cand("#3", false)},
	}
	s := &spy{}
	r, err := Run(context.Background(), observed(f, s, &round.Architecture{Every: 3, Count: 3}))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.calls) != 0 {
		t.Fatalf("observation called mutation adapters: %v", s.calls)
	}
	if len(r.Did) != 0 {
		t.Fatalf("observation reports completed actions: %+v", r.Did)
	}
	var kinds []string
	for _, p := range r.Proposed {
		kinds = append(kinds, p.Action.Action+" "+p.Target)
	}
	want := []string{"reconcile slot-stuck dana", "train ben,#11", "mint ", "assign #2"}
	if len(kinds) != len(want) {
		t.Fatalf("proposed %q, want %q", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("proposed %q, want %q", kinds, want)
		}
	}
	if !hasItem(r.NeedsYou, "candidate-hold", "#3") {
		t.Errorf("an unready candidate must say why it is held: %+v", r.NeedsYou)
	}
}

// AC-4: the first proposal stands on the snapshot; every later one depends on
// earlier hypothetical actions, and unknown checks are named, not verified.
func TestObserveLabelsConditionalAndUnknown(t *testing.T) {
	f := &fake{
		slots:    []Slot{{Name: "ben", State: "done", Issue: "#1", PR: "#10"}, {Name: "dana", State: "idle"}},
		repaired: []Finding{{"slot-stuck", "dana"}},
		cands:    []round.Candidate{cand("#2", true)},
	}
	e := observed(f, &spy{}, &round.Architecture{Every: 3, Count: 3})
	r, err := Run(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Observed || len(r.Proposed) < 2 {
		t.Fatalf("want an observation with several proposals: %+v", r)
	}
	if r.Proposed[0].Conditional {
		t.Errorf("the first proposal depends on nothing earlier: %+v", r.Proposed[0])
	}
	for _, p := range r.Proposed[1:] {
		if !p.Conditional {
			t.Errorf("a later proposal must be conditional: %+v", p)
		}
	}
	for _, p := range r.Proposed {
		if len(p.Unchecked) == 0 {
			t.Errorf("proposal %s %s names no check left to execution", p.Action.Action, p.Target)
		}
	}
	for _, kind := range []string{"awaiting-review", "downstream", "assign"} {
		if !hasKind(r.Unknown, kind) {
			t.Errorf("missing unknown %q: %+v", kind, r.Unknown)
		}
	}
}

// AC-3: for the same snapshot, observation's first proposal is the action
// execution takes first.
func TestObserveFirstActionMatchesExecution(t *testing.T) {
	for name, mk := range map[string]func() *fake{
		"repair first": func() *fake {
			return &fake{repaired: []Finding{{"slot-stuck", "ben"}}, slots: []Slot{{Name: "ben", State: "idle"}}, cands: []round.Candidate{cand("#2", true)}}
		},
		"landing first": func() *fake {
			return &fake{slots: []Slot{{Name: "ben", State: "done", Issue: "#1", PR: "#10"}, {Name: "dana", State: "idle"}}, cands: []round.Candidate{cand("#2", true)}}
		},
		"review missing holds landing": func() *fake {
			return &fake{
				slots:    []Slot{{Name: "ben", State: "done", Issue: "#1", PR: "#10"}, {Name: "dana", State: "idle"}},
				cands:    []round.Candidate{cand("#2", true)},
				awaiting: func(s Slot) string { return "waiting for review-spec, review-quality at head" },
			}
		},
		"assignment first": func() *fake {
			return &fake{slots: []Slot{{Name: "ben", State: "idle"}}, cands: []round.Candidate{cand("#1", false), cand("#2", true)}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			obs, err := Run(context.Background(), observed(mk(), &spy{}, nil))
			if err != nil {
				t.Fatal(err)
			}
			exec, err := Run(context.Background(), mk().env())
			if err != nil {
				t.Fatal(err)
			}
			if len(obs.Proposed) == 0 || len(exec.Did) == 0 {
				t.Fatalf("observed %+v executed %+v", obs.Proposed, exec.Did)
			}
			o, x := obs.Proposed[0].Action, exec.Did[0]
			if x.Action == "merge" {
				x.Action = "gate" // execution records the landed gate as a merge
			}
			if o.Action != x.Action || o.Target != x.Target {
				t.Fatalf("observation first %s %s, execution first %s %s", o.Action, o.Target, x.Action, x.Target)
			}
			if hasItem(exec.NeedsYou, "awaiting-review", "ben") != hasItem(obs.NeedsYou, "awaiting-review", "ben") {
				t.Fatalf("awaiting-review hold differs: observed %+v executed %+v", obs.NeedsYou, exec.NeedsYou)
			}
		})
	}
}

// AC-5: an observation is not approval. Execution after it still runs the
// gate on current state and honours a failure the observation could not see.
func TestExecutionAfterObservationStillGates(t *testing.T) {
	f := &fake{
		slots:   []Slot{{Name: "ben", State: "done", Issue: "#1", PR: "#10"}},
		gateOut: func(string) GateRun { return GateRun{Result: worker.GateResult{Verdict: worker.GateVerifyFailed}} },
	}
	obs, err := Run(context.Background(), observed(f, &spy{}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(obs.Proposed) != 1 || obs.Proposed[0].Action.Action != "gate" {
		t.Fatalf("observation should propose the gate: %+v", obs.Proposed)
	}
	if len(obs.Held) != 0 || len(obs.FailedHeads) != 0 {
		t.Fatalf("observation changed hold bookkeeping: %+v %+v", obs.Held, obs.FailedHeads)
	}
	r, err := Run(context.Background(), f.env())
	if err != nil {
		t.Fatal(err)
	}
	if len(f.gated) != 1 {
		t.Fatalf("execution must run the gate itself, gated %v", f.gated)
	}
	if len(r.Did) != 0 || !hasItem(r.NeedsYou, "gate-failed", "ben") {
		t.Fatalf("a failed gate after observation must not merge: did %+v needsYou %+v", r.Did, r.NeedsYou)
	}
}

func hasItem(items []Item, kind, target string) bool {
	for _, it := range items {
		if it.Kind == kind && it.Target == target {
			return true
		}
	}
	return false
}

func hasKind(items []Item, kind string) bool {
	for _, it := range items {
		if it.Kind == kind {
			return true
		}
	}
	return false
}
