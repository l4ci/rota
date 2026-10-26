package roundtick

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
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
		summary string
		err     error
		want    ReviewOutcome
		warned  bool
	}{
		{name: "nothing waiting", want: ReviewOutcome{}},
		{name: "reports without holding", summary: "1 from rev u/9", want: ReviewOutcome{Pending: true, Detail: "1 from rev u/9"}},
		{name: "poll failure holds nothing", err: boom, want: ReviewOutcome{}, warned: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, warned := reviewSrc(false)
			s.Poll = func(context.Context) (string, error) { return tc.summary, tc.err }
			if got := ReviewStep(context.Background(), s); got != tc.want {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
			if (len(*warned) > 0) != tc.warned {
				t.Fatalf("warned %v", *warned)
			}
		})
	}
}

// reviewFake is the forge side of PendingSummary: a PR in one state with the
// given notes waiting on it.
type reviewFake struct {
	state   string
	viewErr error
	notes   []tracker.Comment
}

func (f reviewFake) MRNotes(context.Context, int) ([]tracker.Comment, error) { return f.notes, nil }
func (f reviewFake) Reviews(context.Context, int) ([]tracker.Review, error)  { return nil, nil }
func (f reviewFake) PRView(context.Context, int) (tracker.PRInfo, error) {
	return tracker.PRInfo{State: f.state}, f.viewErr
}

func TestPendingSummary(t *testing.T) {
	boom := errors.New("boom")
	at := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	note := []tracker.Comment{{ID: "1", Author: "rev", Body: "please rename x", CreatedAt: at}}
	slot := worker.NewSlot("nia", "nia/9-x", "", "main", "")
	slot.SetPR("https://github.com/o/r/pull/9")
	noPR := worker.NewSlot("nia", "nia/9-x", "", "main", "")
	for _, tc := range []struct {
		name string
		f    reviewFake
		s    *worker.Slot
		want string
		err  error
	}{
		{name: "open with input", f: reviewFake{state: "OPEN", notes: note}, s: slot, want: "1 from rev https://github.com/o/r/pull/9"},
		{name: "state case is ignored", f: reviewFake{state: "open", notes: note}, s: slot, want: "1 from rev https://github.com/o/r/pull/9"},
		{name: "open and quiet", f: reviewFake{state: "OPEN"}, s: slot},
		{name: "merged pr", f: reviewFake{state: "MERGED", notes: note}, s: slot},
		{name: "closed pr", f: reviewFake{state: "CLOSED", notes: note}, s: slot},
		{name: "slot gone", f: reviewFake{state: "OPEN", notes: note}},
		{name: "view failure", f: reviewFake{viewErr: boom, notes: note}, s: slot, err: boom},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := PendingSummary(context.Background(), tc.f, tc.s, nil, at)
			if got != tc.want || !errors.Is(err, tc.err) {
				t.Fatalf("got %q, %v want %q, %v", got, err, tc.want, tc.err)
			}
		})
	}
	// A slot without a PR is not polled for review input.
	if got, _ := PendingSummary(context.Background(), reviewFake{state: "OPEN", notes: note}, noPR, nil, at); got != "" {
		t.Fatalf("slot without a PR reported %q", got)
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

// A done slot whose head has no review verdicts is not gated: the gate would
// call a fresh done "stale" before it asked for the verdicts (#2072). The
// slot is reported as waiting for review and the next tick looks again.
func TestTickDoneSlotWithoutReviewVerdictsIsNotGated(t *testing.T) {
	f := &fake{slots: []Slot{{Name: "ben", State: "done", Issue: "#7", PR: "#70"}, {Name: "dana", State: "done", Issue: "#8", PR: "#80"}}}
	e := f.env()
	e.AwaitingReview = func(_ context.Context, s Slot, _ string) string {
		if s.Name == "ben" {
			return "review depth full needs a recorded review verdict"
		}
		return ""
	}
	r, err := Run(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"main:dana"}; !slices.Equal(f.gated, want) {
		t.Fatalf("gated %v, want %v: ben waits for review", f.gated, want)
	}
	if want := []Item{{"awaiting-review", "ben", "review depth full needs a recorded review verdict"}}; !slices.Equal(r.NeedsYou, want) {
		t.Fatalf("needs-you %+v, want %+v", r.NeedsYou, want)
	}
	if len(r.Held) != 0 {
		t.Errorf("waiting for review must not hold the slot: %v", r.Held)
	}
}
