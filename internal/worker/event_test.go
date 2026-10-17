package worker

import (
	"slices"
	"testing"
	"time"
)

// Every declared kind has exactly one table row, and the row names only values
// its consumers understand: a kind added to the consts but not the table, or a
// row with a typo, fails here instead of silently doing nothing.
func TestEventTableCoversEveryKind(t *testing.T) {
	declared := []string{EventBlocked, EventQuestion, EventDone, EventReviewReply, EventHeartbeat}
	if !slices.Equal(EventKinds, declared) {
		t.Fatalf("EventKinds = %v, want %v: a kind is missing from the table", EventKinds, declared)
	}
	seen := map[string]bool{}
	for _, e := range Events {
		if seen[e.Kind] {
			t.Errorf("kind %q has two rows", e.Kind)
		}
		seen[e.Kind] = true
		if !slices.Contains([]string{"", "blocked", "done"}, e.State) {
			t.Errorf("%s: state %q is not a slot state an event records", e.Kind, e.State)
		}
		if !slices.Contains([]string{"", StateBlocked, StateDone}, e.Poll) {
			t.Errorf("%s: poll state %q", e.Kind, e.Poll)
		}
		if e.EveryRow && e.Ledger == "" {
			t.Errorf("%s: EveryRow without a ledger kind", e.Kind)
		}
		if e.Send && e.Help == "" {
			t.Errorf("%s: a send kind needs its flag help", e.Kind)
		}
		if e.SendBody && !e.Send {
			t.Errorf("%s: SendBody on a kind send does not take", e.Kind)
		}
	}
}

// Poll reads the table: a live event decides the row exactly when its kind
// names a poll state, and a heartbeat never does.
func TestDecideFollowsTheEventTable(t *testing.T) {
	base := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	idle := PollRow{"w1", StateIdle, "static pane"}
	for _, spec := range Events {
		t.Run(spec.Kind, func(t *testing.T) {
			ev := &Event{Seq: 1, Kind: spec.Kind, Body: "b", TS: stamp(base)}
			got, decided := decide(ev, idle, base)
			if decided != (spec.Poll != "") {
				t.Fatalf("decided = %v with poll state %q", decided, spec.Poll)
			}
			if decided && got.State != spec.Poll {
				t.Errorf("row state = %s, want %s", got.State, spec.Poll)
			}
		})
	}
}
