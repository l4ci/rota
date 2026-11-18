package hook

import (
	"github.com/l4ci/rota/internal/keepstate"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/stamp"
)

// Both Stop deciders share one handoff-block rule; these tables cross it
// through the exported functions.
func TestStopDecidersHandoffBlock(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	set := Settings{Threshold: 70, StateMaxAge: 600, HandoffMaxAge: 600, HandoffMaxBlks: 2}
	pct := 80.0
	st := func(mod func(*HandoffState)) State {
		s := State{UpdatedAt: stamp.Format(now), ContextPct: &pct}
		if mod != nil {
			mod(&s.OrchestratorHandoff)
			s.GuardHandoff = s.OrchestratorHandoff
		}
		return s
	}
	blockedAt := stamp.Format(now.Add(-time.Minute))
	fresh := keepstate.Handoff{Exists: true, ModTime: now.Add(-10 * time.Second)}
	old := keepstate.Handoff{Exists: true, ModTime: now.Add(-time.Hour)}
	newerOld := keepstate.Handoff{Exists: true, ModTime: now.Add(-30 * time.Second)} // newer than blockedAt
	set2 := set
	set2.HandoffMaxAge = 5 // newerOld is newer than the block but no longer fresh

	type want struct {
		block, persist, failed bool
		blocks                 int
	}
	tests := []struct {
		name   string
		active bool // StopHookActive (DecideStop only)
		state  State
		ho     keepstate.Handoff
		set    Settings
		stop   want
		guard  want
	}{
		{"no handoff blocks and counts per rule", false, st(nil), keepstate.Handoff{},
			set, want{true, true, false, 0}, want{true, true, false, 1}},
		{"re-entered stop counts in both", true, st(nil), keepstate.Handoff{},
			set, want{true, true, false, 1}, want{true, true, false, 1}},
		{"fresh handoff passes", false, st(nil), fresh,
			set, want{}, want{}},
		{"stale handoff does not pass", false, st(nil), old,
			set, want{true, true, false, 0}, want{true, true, false, 1}},
		{"handoff older than last block does not pass", false,
			st(func(s *HandoffState) { s.BlockedAt = stamp.Format(now) }), fresh,
			set, want{true, true, false, 0}, want{true, true, false, 1}},
		{"re-entered: newer but aged handoff passes Stop only", true,
			st(func(s *HandoffState) { s.BlockedAt = blockedAt }), newerOld,
			set2, want{}, want{true, true, false, 1}},
		{"cap reached latches failed", true,
			st(func(s *HandoffState) { s.HandoffBlocks = 2 }), keepstate.Handoff{},
			set, want{false, true, true, 2}, want{false, true, true, 2}},
		{"first stop at cap: Stop never counts, guard latches", false,
			st(func(s *HandoffState) { s.HandoffBlocks = 2 }), keepstate.Handoff{},
			set, want{true, true, false, 2}, want{false, true, true, 2}},
		{"already failed is left alone", true,
			st(func(s *HandoffState) { s.HandoffFailed = true; s.HandoffBlocks = 2 }), keepstate.Handoff{},
			set, want{false, false, true, 2}, want{false, false, true, 2}},
	}
	check := func(t *testing.T, who string, d StopDecision, budget HandoffState, w want) {
		t.Helper()
		if d.Block != w.block || d.Persist != w.persist || budget.HandoffFailed != w.failed || budget.HandoffBlocks != w.blocks {
			t.Errorf("%s = block %v persist %v failed %v blocks %d, want %+v", who,
				d.Block, d.Persist, budget.HandoffFailed, budget.HandoffBlocks, w)
		}
		if d.Block && (d.Reason == "" || budget.BlockedAt != stamp.Format(now)) {
			t.Errorf("%s block lacks reason or BlockedAt stamp: %+v", who, d)
		}
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stop := DecideStop(StopIn{StopHookActive: tc.active}, tc.state, tc.set, tc.ho, "/h", now)
			check(t, "DecideStop", stop, stop.State.OrchestratorHandoff, tc.stop)
			guard := DecideGuardStop(tc.state, tc.set, tc.ho, now)
			check(t, "DecideGuardStop", guard, guard.State.GuardHandoff, tc.guard)
			if stop.State.GuardHandoff != tc.state.GuardHandoff || guard.State.OrchestratorHandoff != tc.state.OrchestratorHandoff {
				t.Fatal("decider changed the other role's handoff history")
			}
		})
	}
}

func TestStopDecidersOwnHandoffCounters(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	set := Settings{Threshold: 70, StateMaxAge: 600, HandoffMaxAge: 600, HandoffMaxBlks: 2}
	pct := 80.0
	st := State{UpdatedAt: stamp.Format(now), ContextPct: &pct}
	for i := 0; i < 3; i++ {
		st = DecideStop(StopIn{StopHookActive: true}, st, set, keepstate.Handoff{}, "/h", now).State
	}
	guard := DecideGuardStop(st, set, keepstate.Handoff{}, now)
	if !guard.Block || !guard.Persist {
		t.Fatalf("orchestrator's exhausted budget suppressed the guard's first block: %+v", guard)
	}
	st = guard.State
	st.ContextPct = new(float64) // a new guard crossing must not reset the orchestrator
	reset := DecideGuardStop(st, set, keepstate.Handoff{}, now)
	st = reset.State
	st.ContextPct = &pct
	if d := DecideStop(StopIn{StopHookActive: true}, st, set, keepstate.Handoff{}, "/h", now); d.Block || d.Persist {
		t.Fatalf("guard crossing reset the orchestrator's exhausted budget: %+v", d)
	}
}

func TestGuardStopCrossing(t *testing.T) {
	set := Settings{Threshold: 75, StateMaxAge: 120, HandoffMaxAge: 900, HandoffMaxBlks: 2}
	st := guardState(80, t0.Format(time.RFC3339))
	for i := 0; i < 3; i++ {
		st = DecideGuardStop(st, set, keepstate.Handoff{}, t0).State
	}
	if d := DecideStop(StopIn{StopHookActive: true}, st, set, keepstate.Handoff{}, "/h", t0); !d.Block || d.State.OrchestratorHandoff.HandoffBlocks != 1 {
		t.Fatalf("guard's exhausted budget suppressed the orchestrator: %+v", d)
	}
	st.ContextPct = pf(10)
	reset := DecideGuardStop(st, set, keepstate.Handoff{}, t0)
	if reset.Block || !reset.Persist || reset.State.GuardHandoff != (HandoffState{}) {
		t.Fatalf("guard crossing did not reset: %+v", reset)
	}
	st = reset.State
	st.ContextPct = pf(80)
	d := DecideGuardStop(st, set, keepstate.Handoff{}, t0)
	if !d.Block || d.State.GuardHandoff.HandoffBlocks != 1 || !strings.Contains(d.Reason, "/rota-pause") {
		t.Fatalf("guard crossing did not restart: %+v", d)
	}
	st.UpdatedAt = t0.Add(-3 * time.Minute).Format(time.RFC3339)
	if d := DecideGuardStop(st, set, keepstate.Handoff{}, t0); d.Block || d.Persist {
		t.Fatalf("guard acted on stale reading: %+v", d)
	}
}

func TestStopDecidersOwnBlockTimes(t *testing.T) {
	set := Settings{Threshold: 75, StateMaxAge: 120, HandoffMaxAge: 900, HandoffMaxBlks: 2}
	st := guardState(80, t0.Format(time.RFC3339))
	old := HandoffState{BlockedAt: stamp.Format(t0.Add(-time.Minute))}
	recent := HandoffState{BlockedAt: stamp.Format(t0)}
	ho := keepstate.Handoff{Exists: true, ModTime: t0.Add(-30 * time.Second)}
	for _, stopBlocked := range []bool{false, true} {
		st.OrchestratorHandoff, st.GuardHandoff = old, recent
		if stopBlocked {
			st.OrchestratorHandoff, st.GuardHandoff = recent, old
		}
		stop := DecideStop(StopIn{StopHookActive: true}, st, set, ho, "/h", t0)
		guard := DecideGuardStop(st, set, ho, t0)
		if stop.Block != stopBlocked || guard.Block == stopBlocked {
			t.Fatalf("other role's timestamp affected handoff freshness: stop=%+v guard=%+v", stop, guard)
		}
	}
}
