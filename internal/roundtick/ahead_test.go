package roundtick

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/landing"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
)

// #4247 AC-1, AC-2: a done slot whose PR is a merge target takes the next
// ready candidate before the merge, and its PR lands as the queued ref, never
// as a gate of the slot's new branch.
func TestAssignAheadFillsDoneTargetBeforeMerge(t *testing.T) {
	f := &fake{
		slots: []Slot{{Name: "wolf", State: "done", Issue: "#1", PR: "#11"}},
		cands: []round.Candidate{cand("#5", true)},
	}
	e := f.env()
	f.withAhead(&e)
	r, err := Run(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"assign #5", "merge #11"}; !reflect.DeepEqual(f.audit, want) {
		t.Fatalf("audit %v, want %v (the assign comes before the merge)", f.audit, want)
	}
	if want := []string{"main:#11"}; !reflect.DeepEqual(f.gated, want) {
		t.Fatalf("gated %v, want %v (the queued PR, not the slot)", f.gated, want)
	}
	if want := []string{"#5->wolf"}; !reflect.DeepEqual(f.ahead, want) || len(f.assigned) != 0 {
		t.Fatalf("ahead %v assigned %v, want %v and no idle assign", f.ahead, f.assigned, want)
	}
	if len(r.NeedsYou) != 0 {
		t.Fatalf("needs you %+v", r.NeedsYou)
	}
}

// An unreadable head keeps the done slot on its current issue, just as it
// keeps that PR out of the gate. This also applies without a previous failure.
func TestAssignAheadUnreadableHeadKeepsDoneSlot(t *testing.T) {
	for _, readErr := range []error{errors.New("forge unavailable\nretry later"), nil} {
		for _, failed := range []bool{false, true} {
			name := "empty head"
			if readErr != nil {
				name = "read error"
			}
			if failed {
				name += "/previous failure"
			}
			t.Run(name, func(t *testing.T) {
				slot := Slot{Name: "wolf", State: "done", Issue: "#1", PR: "#11"}
				f := &fake{slots: []Slot{slot}, cands: []round.Candidate{cand("#5", true)}}
				e := f.env()
				f.withAhead(&e)
				e.Head = func(context.Context, string) (string, error) { return "", readErr }
				previous := FailedHead{Head: "old-head", Why: "verify-failed: tests failed"}
				if failed {
					e.FailedHeads = map[string]FailedHead{"wolf": previous}
				}
				r, err := Run(context.Background(), e)
				if err != nil {
					t.Fatal(err)
				}
				if len(f.ahead)+len(f.assigned)+len(f.gated)+len(f.trained) != 0 || f.slots[0] != slot || len(f.queued) != 0 {
					t.Fatalf("unreadable head changed assignment or landed: ahead=%v assigned=%v gated=%v trained=%v slots=%+v queued=%v", f.ahead, f.assigned, f.gated, f.trained, f.slots, f.queued)
				}
				why := "cannot read PR head; not retrying the gate"
				if readErr != nil {
					why += ": forge unavailable"
				}
				if want := []Item{{Kind: "gate-failed", Target: "wolf", Why: why}}; !reflect.DeepEqual(r.NeedsYou, want) {
					t.Fatalf("needs you %+v, want %+v", r.NeedsYou, want)
				}
				if failed && r.FailedHeads["wolf"] != previous {
					t.Fatalf("unreadable head forgot previous failure: %v", r.FailedHeads)
				}
			})
		}
	}
}

func TestAssignAheadSharesMergeReadiness(t *testing.T) {
	for _, tc := range []struct {
		name        string
		failed      map[string]FailedHead
		trainFill   bool
		bypass      bool
		wantBlocked bool
	}{
		{name: "held slot head", failed: map[string]FailedHead{"wolf": {Head: "current", Why: "verify-failed"}}, wantBlocked: true},
		{name: "held head follows PR", failed: map[string]FailedHead{"#11": {Head: "current", Why: "verify-failed"}}, wantBlocked: true},
		{name: "new head clears failure", failed: map[string]FailedHead{"wolf": {Head: "old", Why: "verify-failed"}}},
		{name: "not ready for fill", trainFill: true, wantBlocked: true},
		{name: "fill bypass follows queued PR", trainFill: true, bypass: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fake{
				slots: []Slot{{Name: "wolf", State: "done", Issue: "#1", PR: "#11"}},
				cands: []round.Candidate{cand("#5", true)},
			}
			e := f.env()
			f.withAhead(&e)
			e.Head = func(context.Context, string) (string, error) { return "current", nil }
			e.FailedHeads = tc.failed
			if tc.trainFill {
				e.TrainFill = roundcfg.TrainFill{Max: 4}
				e.TrainReady = func(string) bool { return false }
				e.FillBypass = func(target string) bool { return tc.bypass && target == "wolf" }
			}
			r, err := Run(context.Background(), e)
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantBlocked {
				if len(f.ahead)+len(f.gated)+len(f.trained) != 0 || len(r.NeedsYou) != 1 {
					t.Fatalf("blocked target assigned or landed: ahead=%v gated=%v trained=%v needsYou=%+v", f.ahead, f.gated, f.trained, r.NeedsYou)
				}
			} else if !slices.Equal(f.ahead, []string{"#5->wolf"}) || !slices.Equal(f.gated, []string{"main:#11"}) || len(r.NeedsYou) != 0 {
				t.Fatalf("due target not assigned and landed: ahead=%v gated=%v needsYou=%+v", f.ahead, f.gated, r.NeedsYou)
			}
		})
	}
}

// AC-3: a done slot whose review input went back to its worker, whose review
// waits on a person, or whose verdicts are missing is not a merge target, so
// it is not assigned ahead; the approved one beside it is.
func TestAssignAheadSkipsRelayedAndAwaitingReview(t *testing.T) {
	f := &fake{
		slots: []Slot{
			{Name: "a", State: "done", Issue: "#1", PR: "#11"},
			{Name: "b", State: "done", Issue: "#2", PR: "#12"},
			{Name: "c", State: "done", Issue: "#3", PR: "#13"},
			{Name: "d", State: "done", Issue: "#4", PR: "#14"},
		},
		cands:    []round.Candidate{cand("#5", true), cand("#6", true), cand("#7", true), cand("#8", true)},
		awaiting: func(s Slot) string { return map[string]string{"b": "no verdict at head"}[s.Name] },
	}
	e := f.env()
	e.ReviewLoop = func(_ context.Context, s Slot) ReviewOutcome {
		switch s.Name {
		case "a":
			return ReviewOutcome{Relayed: true, Detail: "1 item"}
		case "c":
			return ReviewOutcome{Pending: true, Hold: true, Detail: "at the bounce cap"}
		}
		return ReviewOutcome{}
	}
	f.withAhead(&e)
	if _, err := Run(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if want := []string{"#5->d"}; !reflect.DeepEqual(f.ahead, want) {
		t.Fatalf("ahead %v, want %v (only the merge target)", f.ahead, want)
	}
}

// A refusal leaves the slot a slot target: the tick goes on and gates it as
// before. A slot-side refusal does not burn the other candidates.
func TestAssignAheadRefusalKeepsSlotTarget(t *testing.T) {
	for _, by := range []string{blockedby.SlotBusy, blockedby.Batch, blockedby.Quota, landing.GateRunning} {
		t.Run(by, func(t *testing.T) {
			f := &fake{
				slots: []Slot{{Name: "wolf", State: "done", Issue: "#1", PR: "#11"}},
				cands: []round.Candidate{cand("#5", true), cand("#6", true)},
			}
			var tried []string
			f.aheadFn = func(slot, id string) error {
				tried = append(tried, id+"->"+slot)
				return &round.BlockedError{By: by, Msg: "refused"}
			}
			e := f.env()
			f.withAhead(&e)
			if _, err := Run(context.Background(), e); err != nil {
				t.Fatal(err)
			}
			if want := []string{"#5->wolf"}; !reflect.DeepEqual(tried, want) {
				t.Fatalf("tried %v, want %v (one try, then the slot is left alone)", tried, want)
			}
			if by != landing.GateRunning {
				if want := []string{"main:wolf"}; !reflect.DeepEqual(f.gated, want) {
					t.Fatalf("gated %v, want %v (still a slot target)", f.gated, want)
				}
			}
		})
	}
}

// A candidate-side refusal (the item is not ready after all) tries the next
// candidate on the same slot.
func TestAssignAheadCandidateRefusalTriesNext(t *testing.T) {
	f := &fake{
		slots: []Slot{{Name: "wolf", State: "done", Issue: "#1", PR: "#11"}},
		cands: []round.Candidate{cand("#5", true), cand("#6", true)},
	}
	f.aheadFn = func(_, id string) error {
		if id == "#5" {
			return &round.BlockedError{By: blockedby.NotReady, Msg: "claimed meanwhile"}
		}
		return nil
	}
	e := f.env()
	f.withAhead(&e)
	if _, err := Run(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if want := []string{"#6->wolf"}; !reflect.DeepEqual(f.ahead, want) {
		t.Fatalf("ahead %v, want %v", f.ahead, want)
	}
	if want := []string{"main:#11"}; !reflect.DeepEqual(f.gated, want) {
		t.Fatalf("gated %v, want %v", f.gated, want)
	}
}

// When the assign went through but the queue holds no record of the slot's
// PR, the target is dropped for this tick and reported, never gated by slot
// name (the slot now holds the new branch).
func TestAssignAheadMissingQueuedRecordDropsTarget(t *testing.T) {
	f := &fake{
		slots:   []Slot{{Name: "wolf", State: "done", Issue: "#1", PR: "#11"}},
		cands:   []round.Candidate{cand("#5", true)},
		noQueue: true,
	}
	e := f.env()
	f.withAhead(&e)
	r, err := Run(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.gated)+len(f.trained) != 0 {
		t.Fatalf("gated %v trained %v, want nothing", f.gated, f.trained)
	}
	if got := needs(r, "assign-ahead"); len(got) != 1 || !strings.Contains(got[0], "#11") {
		t.Fatalf("needs you %+v, want one assign-ahead item naming #11", r.NeedsYou)
	}
}

// AC-7: a merge that refuses or holds the queued PR keeps the assignment.
func TestAssignAheadSurvivesMergeHold(t *testing.T) {
	f := &fake{
		slots: []Slot{{Name: "wolf", State: "done", Issue: "#1", PR: "#11"}},
		cands: []round.Candidate{cand("#5", true)},
		gateOut: func(string) GateRun {
			return GateRun{Result: landing.GateResult{Verdict: landing.GateVerifyFailed, Err: "tests red"}}
		},
	}
	e := f.env()
	f.withAhead(&e)
	r, err := Run(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(r.Did, func(a Action) bool { return a.Action == "assign" && a.Target == "#5" }) {
		t.Fatalf("did %+v, want the assign kept", r.Did)
	}
	if got := needs(r, "gate-failed"); len(got) != 1 || !strings.HasPrefix(got[0], "#11:") {
		t.Fatalf("needs you %+v, want #11 gate-failed", r.NeedsYou)
	}
}

// AC-7: a stop seen after assign-ahead halts the merge and keeps the assignment.
func TestStopAfterAssignAheadKeepsAssignment(t *testing.T) {
	f := &fake{
		slots: []Slot{{Name: "wolf", State: "done", Issue: "#1", PR: "#11"}},
		cands: []round.Candidate{cand("#5", true)},
	}
	stopped := false
	f.aheadFn = func(string, string) error { stopped = true; return nil }
	e := f.env()
	e.Stop = func() error { return StopError(stopped, "") }
	f.withAhead(&e)
	r, err := Run(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(r.Stopped, ErrStopped) {
		t.Fatalf("stopped %v", r.Stopped)
	}
	if len(f.gated)+len(f.trained) != 0 {
		t.Fatalf("gated %v trained %v after the stop", f.gated, f.trained)
	}
	if want := []string{"assign #5"}; !reflect.DeepEqual(f.audit, want) {
		t.Fatalf("audit %v, want %v", f.audit, want)
	}
}

// AC-4: assign-ahead and the idle assign share round.autopilotCap.
func TestAssignAheadSharesCap(t *testing.T) {
	f := &fake{
		slots: []Slot{{Name: "wolf", State: "done", Issue: "#1", PR: "#11"}, {Name: "ben", State: "idle"}, {Name: "dana", State: "idle"}},
		cands: []round.Candidate{cand("#5", true), cand("#6", true), cand("#7", true)},
	}
	e := f.env()
	e.Cap = 2
	f.withAhead(&e)
	if _, err := Run(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if want := []string{"#5->wolf"}; !reflect.DeepEqual(f.ahead, want) {
		t.Fatalf("ahead %v, want %v", f.ahead, want)
	}
	if want := []string{"#6"}; !reflect.DeepEqual(f.assigned, want) {
		t.Fatalf("assigned %v, want %v (the cap less the one ahead)", f.assigned, want)
	}
}

// AC-4: a full batch or the quota cap holds assign-ahead as it holds assign;
// with neither, the same round assigns ahead.
func TestAssignAheadHonorsBatchAndQuota(t *testing.T) {
	for _, tc := range []struct {
		name      string
		batch     bool
		capped    bool
		wantAhead bool
	}{{"free", false, false, true}, {"batch full", true, false, false}, {"quota capped", false, true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fake{
				slots: []Slot{{Name: "wolf", State: "done", Issue: "#1", PR: "#11"}},
				cands: []round.Candidate{cand("#5", true)},
			}
			e := f.env()
			if tc.batch {
				e.Batch, e.OpenItems = 1, func(context.Context) int { return 1 }
			}
			if tc.capped {
				e.Capped = func(context.Context) string { return "every account cools down" }
			}
			f.withAhead(&e)
			if _, err := Run(context.Background(), e); err != nil {
				t.Fatal(err)
			}
			if got := len(f.ahead) > 0; got != tc.wantAhead {
				t.Fatalf("ahead %v, want ahead=%v", f.ahead, tc.wantAhead)
			}
			if !tc.wantAhead && !reflect.DeepEqual(f.gated, []string{"main:wolf"}) {
				t.Fatalf("gated %v, want the slot target unchanged", f.gated)
			}
		})
	}
}

// Observation proposes the assign-ahead, the merge of the queued ref and the
// idle assign, and never counts a candidate or a slot twice.
func TestObserveAssignAheadCountsSlotOnce(t *testing.T) {
	f := &fake{
		slots: []Slot{{Name: "wolf", State: "done", Issue: "#1", PR: "#11"}, {Name: "ben", State: "idle"}},
		cands: []round.Candidate{cand("#5", true), cand("#6", true)},
	}
	e := f.env()
	e.Observe = true
	f.withAhead(&e)
	r, err := Run(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range r.Proposed {
		got = append(got, p.Action.Action+" "+p.Target)
	}
	if want := []string{"assign #5", "gate #11", "assign #6"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("proposed %v, want %v", got, want)
	}
	if len(f.ahead)+len(f.assigned)+len(f.gated) != 0 {
		t.Fatalf("observation acted: ahead %v assigned %v gated %v", f.ahead, f.assigned, f.gated)
	}
}

// Without the AssignTo port the tick gates done slots by name, as before.
func TestNoAssignToPortGatesSlotByName(t *testing.T) {
	f := &fake{
		slots: []Slot{{Name: "wolf", State: "done", Issue: "#1", PR: "#11"}},
		cands: []round.Candidate{cand("#5", true)},
	}
	if _, err := Run(context.Background(), f.env()); err != nil {
		t.Fatal(err)
	}
	if want := []string{"main:wolf"}; !reflect.DeepEqual(f.gated, want) {
		t.Fatalf("gated %v, want %v", f.gated, want)
	}
}

// assignBoth is an AssignTo that takes the named slot and a second one, as a
// best-of:2 issue does: each slot's PR joins the queue and the slot works.
func assignBoth(f *fake, second string) func(context.Context, string, string, []string) ([]string, error) {
	return func(_ context.Context, slot, id string, _ []string) ([]string, error) {
		f.ahead = append(f.ahead, id+"->"+slot+"+"+second)
		for i := range f.slots {
			if n := f.slots[i].Name; n == slot || n == second {
				if f.slots[i].PR != "" {
					f.queued = append(f.queued, f.slots[i].PR)
				}
				f.slots[i] = Slot{Name: n, State: "working", Issue: id}
			}
		}
		return []string{slot, second}, nil
	}
}

// Relay 2, item 1: a best-of:2 assign that parks two done merge targets swaps
// both for their queued PRs, and the second slot is not assigned again.
func TestAssignAheadBestOfSwapsBothSlots(t *testing.T) {
	f := &fake{
		slots: []Slot{{Name: "wolf", State: "done", Issue: "#1", PR: "#11"}, {Name: "fox", State: "done", Issue: "#2", PR: "#12"}},
		cands: []round.Candidate{cand("#5", true), cand("#6", true)},
	}
	e := f.env()
	e.AssignTo = assignBoth(f, "fox")
	if _, err := Run(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if want := []string{"#5->wolf+fox"}; !reflect.DeepEqual(f.ahead, want) {
		t.Fatalf("ahead %v, want %v (fox taken by the best-of)", f.ahead, want)
	}
	if want := [][]string{{"main", "#11", "#12"}}; !reflect.DeepEqual(f.trained, want) || len(f.gated) != 0 {
		t.Fatalf("trained %v gated %v, want %v (both queued refs, no slot name)", f.trained, f.gated, want)
	}
}

// A best-of second slot that was not a merge target (awaiting review) is not
// added to this tick's merge.
func TestAssignAheadBestOfLeavesNonTargetOut(t *testing.T) {
	f := &fake{
		slots:    []Slot{{Name: "wolf", State: "done", Issue: "#1", PR: "#11"}, {Name: "fox", State: "done", Issue: "#2", PR: "#12"}},
		cands:    []round.Candidate{cand("#5", true)},
		awaiting: func(s Slot) string { return map[string]string{"fox": "no verdict at head"}[s.Name] },
	}
	e := f.env()
	e.AssignTo = assignBoth(f, "fox")
	if _, err := Run(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if want := []string{"main:#11"}; !reflect.DeepEqual(f.gated, want) || len(f.trained) != 0 {
		t.Fatalf("gated %v trained %v, want %v", f.gated, f.trained, want)
	}
}

// Relay 2, item 2: an assign refused after it queued the slot's PR still
// swaps the target, so the PR lands this tick by its ref, not by slot name.
func TestAssignAheadRefusalAfterQueueSwapsTarget(t *testing.T) {
	f := &fake{
		slots: []Slot{{Name: "wolf", State: "done", Issue: "#1", PR: "#11"}},
		cands: []round.Candidate{cand("#5", true)},
	}
	e := f.env()
	e.AssignTo = func(_ context.Context, slot, id string, _ []string) ([]string, error) {
		f.queued = append(f.queued, "#11")
		f.slots[0] = Slot{Name: "wolf", State: "idle"}
		return nil, &round.BlockedError{By: blockedby.ClaimLost, Msg: "lost the claim race"}
	}
	r, err := Run(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"main:#11"}; !reflect.DeepEqual(f.gated, want) {
		t.Fatalf("gated %v, want %v", f.gated, want)
	}
	if got := needs(r, "assign-ahead"); len(got) != 0 {
		t.Fatalf("needs you %v: a queued PR is not missing", got)
	}
}
