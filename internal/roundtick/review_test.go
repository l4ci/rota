package roundtick

import (
	"context"
	"slices"
	"testing"
)

func TestTickReviewRelaysAndSkipsTheMerge(t *testing.T) {
	f := &fake{slots: []Slot{
		{Name: "ben", State: "done", Issue: "#1", PR: "u/1"},
		{Name: "dana", State: "done", Issue: "#2", PR: "u/2"},
	}}
	e := f.env()
	e.ReviewLoop = func(_ context.Context, s Slot) ReviewOutcome {
		if s.Name == "ben" {
			return ReviewOutcome{Relayed: true, Detail: "2 item(s), bounce 1"}
		}
		return ReviewOutcome{}
	}
	r, err := Run(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.gated) != 1 || f.gated[0] != "main:dana" {
		t.Fatalf("only the slot without review input merges, gated %v", f.gated)
	}
	if !slices.Contains(f.audit, "review-relay ben") {
		t.Fatalf("audit %v", f.audit)
	}
	if len(r.NeedsYou) != 0 {
		t.Fatalf("a relayed slot needs nobody: %+v", r.NeedsYou)
	}
}

func TestTickHeldReviewNeedsYouAndIsNotMerged(t *testing.T) {
	f := &fake{slots: []Slot{{Name: "ben", State: "done", Issue: "#1", PR: "u/1"}}}
	e := f.env()
	e.ReviewLoop = func(context.Context, Slot) ReviewOutcome {
		return ReviewOutcome{Pending: true, Hold: true, Detail: "1 from rev"}
	}
	r, err := Run(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.gated) != 0 || len(f.trained) != 0 {
		t.Fatalf("merged a held PR: %v %v", f.gated, f.trained)
	}
	if len(r.NeedsYou) != 1 || r.NeedsYou[0].Kind != "review" || r.NeedsYou[0].Target != "ben" || len(r.New) != 1 {
		t.Fatalf("needs-you %+v new %+v", r.NeedsYou, r.New)
	}
}

func TestTickWithoutReviewLoopMergesAsBefore(t *testing.T) {
	f := &fake{slots: []Slot{{Name: "ben", State: "done", Issue: "#1", PR: "u/1"}}}
	if _, err := Run(context.Background(), f.env()); err != nil || len(f.gated) != 1 {
		t.Fatalf("gated %v %v", f.gated, err)
	}
}

// Under manual the review is a report: the tick lists it and the gate runs as
// before, so an unmarked PR comment never stalls the autopilot.
func TestTickManualPendingReviewStillGates(t *testing.T) {
	f := &fake{slots: []Slot{{Name: "ben", State: "done", Issue: "#1", PR: "u/1"}}}
	e := f.env()
	e.ReviewLoop = func(context.Context, Slot) ReviewOutcome {
		return ReviewOutcome{Pending: true, Detail: "1 from rev"}
	}
	r, err := Run(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.gated) != 1 || f.gated[0] != "main:ben" {
		t.Fatalf("manual review input blocked the merge: gated %v", f.gated)
	}
	if len(r.NeedsYou) != 1 || r.NeedsYou[0].Kind != "review" || r.NeedsYou[0].Target != "ben" {
		t.Fatalf("needs-you %+v", r.NeedsYou)
	}
}
