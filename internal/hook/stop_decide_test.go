package hook

import (
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
	st := func(mod func(*State)) State {
		s := State{UpdatedAt: stamp.Format(now), ContextPct: &pct}
		if mod != nil {
			mod(&s)
		}
		return s
	}
	blockedAt := stamp.Format(now.Add(-time.Minute))
	fresh := Handoff{Exists: true, ModTime: now.Add(-10 * time.Second)}
	old := Handoff{Exists: true, ModTime: now.Add(-time.Hour)}
	newerOld := Handoff{Exists: true, ModTime: now.Add(-30 * time.Second)} // newer than blockedAt
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
		ho     Handoff
		set    Settings
		stop   want
		guard  want
	}{
		{"no handoff blocks and counts per rule", false, st(nil), Handoff{},
			set, want{true, true, false, 0}, want{true, true, false, 1}},
		{"re-entered stop counts in both", true, st(nil), Handoff{},
			set, want{true, true, false, 1}, want{true, true, false, 1}},
		{"fresh handoff passes", false, st(nil), fresh,
			set, want{}, want{}},
		{"stale handoff does not pass", false, st(nil), old,
			set, want{true, true, false, 0}, want{true, true, false, 1}},
		{"handoff older than last block does not pass", false,
			st(func(s *State) { s.BlockedAt = stamp.Format(now) }), fresh,
			set, want{true, true, false, 0}, want{true, true, false, 1}},
		{"re-entered: newer but aged handoff passes Stop only", true,
			st(func(s *State) { s.BlockedAt = blockedAt }), newerOld,
			set2, want{}, want{true, true, false, 1}},
		{"cap reached latches failed", true,
			st(func(s *State) { s.HandoffBlocks = 2 }), Handoff{},
			set, want{false, true, true, 2}, want{false, true, true, 2}},
		{"first stop at cap: Stop never counts, guard latches", false,
			st(func(s *State) { s.HandoffBlocks = 2 }), Handoff{},
			set, want{true, true, false, 2}, want{false, true, true, 2}},
		{"already failed is left alone", true,
			st(func(s *State) { s.HandoffFailed = true; s.HandoffBlocks = 2 }), Handoff{},
			set, want{false, false, true, 2}, want{false, false, true, 2}},
	}
	check := func(t *testing.T, who string, d StopDecision, w want) {
		t.Helper()
		if d.Block != w.block || d.Persist != w.persist || d.State.HandoffFailed != w.failed || d.State.HandoffBlocks != w.blocks {
			t.Errorf("%s = block %v persist %v failed %v blocks %d, want %+v", who,
				d.Block, d.Persist, d.State.HandoffFailed, d.State.HandoffBlocks, w)
		}
		if d.Block && (d.Reason == "" || d.State.BlockedAt != stamp.Format(now)) {
			t.Errorf("%s block lacks reason or BlockedAt stamp: %+v", who, d)
		}
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			check(t, "DecideStop", DecideStop(StopIn{StopHookActive: tc.active}, tc.state, tc.set, tc.ho, "/h", now), tc.stop)
			check(t, "DecideGuardStop", DecideGuardStop(tc.state, tc.set, tc.ho, now), tc.guard)
		})
	}
}
