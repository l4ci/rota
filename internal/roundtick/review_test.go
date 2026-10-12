package roundtick

import (
	"context"
	"errors"
	"fmt"
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

// A held slot with no Pending outcome (a failed poll) is not merged and adds
// no item: the caller already warned.
func TestTickHeldWithoutPendingIsSilent(t *testing.T) {
	f := &fake{slots: []Slot{{Name: "ben", State: "done", Issue: "#1", PR: "u/1"}}}
	e := f.env()
	e.ReviewLoop = func(context.Context, Slot) ReviewOutcome { return ReviewOutcome{Hold: true} }
	r, err := Run(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.gated) != 0 || len(f.trained) != 0 || len(r.NeedsYou) != 0 {
		t.Fatalf("merged %v %v or reported %+v", f.gated, f.trained, r.NeedsYou)
	}
}

func reviewSrc(auto bool) (ReviewSources, *[]string) {
	var warned []string
	return ReviewSources{
		Slot: "nia", PR: "u/9", Auto: auto, MaxBounces: "3",
		Warn: func(f string, a ...any) { warned = append(warned, fmt.Sprintf(f, a...)) },
	}, &warned
}

func TestReviewStepManual(t *testing.T) {
	boom := errors.New("boom")
	for _, tc := range []struct {
		name    string
		open    bool
		openErr error
		pending string
		pendErr error
		want    ReviewOutcome
		warned  bool
	}{
		{name: "closed pr", want: ReviewOutcome{}},
		{name: "nothing waiting", open: true, want: ReviewOutcome{}},
		{name: "reports without holding", open: true, pending: "1 from rev u/9", want: ReviewOutcome{Pending: true, Detail: "1 from rev u/9"}},
		{name: "view failure holds nothing", openErr: boom, want: ReviewOutcome{}, warned: true},
		{name: "poll failure holds nothing", open: true, pendErr: boom, want: ReviewOutcome{}, warned: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, warned := reviewSrc(false)
			s.Open = func(context.Context) (bool, error) { return tc.open, tc.openErr }
			s.Pending = func(context.Context) (string, error) { return tc.pending, tc.pendErr }
			if got := ReviewStep(context.Background(), s); got != tc.want {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
			if (len(*warned) > 0) != tc.warned {
				t.Fatalf("warned %v", *warned)
			}
		})
	}
}

func TestReviewStepAuto(t *testing.T) {
	boom := errors.New("boom")
	for _, tc := range []struct {
		name   string
		setup  error
		res    RelayResult
		err    error
		want   ReviewOutcome
		warned bool
	}{
		{name: "nothing", res: RelayResult{Nothing: true}, want: ReviewOutcome{}},
		{name: "relayed", res: RelayResult{Items: 2, Bounces: 1}, want: ReviewOutcome{Relayed: true, Detail: "2 item(s), bounce 1 of 3 u/9"}},
		{name: "at the cap", res: RelayResult{Items: 2, AtCap: true}, err: boom, want: ReviewOutcome{Pending: true, Hold: true, Detail: "2 item(s) waiting on u/9; the item is at the bounce cap"}},
		{name: "poll failed", err: boom, want: ReviewOutcome{Hold: true}, warned: true},
		{name: "setup failed", setup: boom, want: ReviewOutcome{Hold: true}, warned: true},
		{name: "relay refused", res: RelayResult{Items: 1}, err: boom, want: ReviewOutcome{Pending: true, Hold: true, Detail: "1 item(s) not relayed u/9: run `rota round review-relay nia` for the error"}, warned: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, warned := reviewSrc(true)
			s.SetupErr = tc.setup
			s.Relay = func(context.Context) (RelayResult, error) { return tc.res, tc.err }
			got := ReviewStep(context.Background(), s)
			if got != tc.want {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
			if (len(*warned) > 0) != tc.warned {
				t.Fatalf("warned %v", *warned)
			}
		})
	}
}
