package hook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/keepstate"
)

func TestReadStateLegacyHandoffCounters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.json")
	// A real pre-split state file has no evidence of which role owned the budget.
	const legacy = `{"sessionId":"old","cwd":"/repo","updatedAt":"2026-10-03T12:00:00Z","contextPct":80,"raw":{"old":true},"handoffBlocks":1,"blockedAt":"2026-10-03T11:59:59Z","guardWarned":true}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	st, found, err := ReadState(path)
	if err != nil || !found {
		t.Fatalf("legacy state: found=%v, err=%v", found, err)
	}
	want := HandoffState{HandoffBlocks: 1, BlockedAt: "2026-10-03T11:59:59Z"}
	if st.OrchestratorHandoff != want || st.GuardHandoff != want || st.SessionID != "old" || st.Cwd != "/repo" || !st.GuardWarned || string(st.Raw) != `{"old":true}` {
		t.Fatalf("legacy fields lost: %+v", st)
	}
	set := Settings{Threshold: 75, StateMaxAge: 120, HandoffMaxAge: 900, HandoffMaxBlks: 2}
	stop := DecideStop(StopIn{StopHookActive: true}, st, set, keepstate.Handoff{}, "/h", t0)
	guard := DecideGuardStop(st, set, keepstate.Handoff{}, t0)
	if !stop.Block || stop.State.OrchestratorHandoff.HandoffBlocks != 2 || !guard.Block || guard.State.GuardHandoff.HandoffBlocks != 2 {
		t.Fatalf("legacy budget not continued: stop=%+v guard=%+v", stop, guard)
	}
	// Persist different histories, then ensure decoding does not merge them again.
	st = guard.State
	if err := UpdateState(path, func(State, bool) State { return st }); err != nil {
		t.Fatal(err)
	}
	loaded, _, err := ReadState(path)
	if err != nil || loaded.OrchestratorHandoff != st.OrchestratorHandoff || loaded.GuardHandoff != st.GuardHandoff {
		t.Fatalf("owned histories changed on round-trip: %+v, err=%v", loaded, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["handoffBlocks"] != nil || fields["blockedAt"] != nil || fields["handoffFailed"] != nil {
		t.Fatalf("rewritten state retained unowned counters: %s", data)
	}
}

func TestStateOwnedHandoffOverridesLegacy(t *testing.T) {
	var st State
	if err := json.Unmarshal([]byte(`{"handoffBlocks":2,"handoffFailed":true,"orchestratorHandoff":{},"guardHandoff":{"handoffBlocks":1}}`), &st); err != nil {
		t.Fatal(err)
	}
	if st.OrchestratorHandoff != (HandoffState{}) || st.GuardHandoff != (HandoffState{HandoffBlocks: 1}) {
		t.Fatalf("legacy fields overrode owned budgets: %+v", st)
	}
}

func TestStateLegacyHandoffFailure(t *testing.T) {
	var st State
	if err := json.Unmarshal([]byte(`{"updatedAt":"2026-10-03T12:00:00Z","contextPct":80,"handoffBlocks":2,"handoffFailed":true}`), &st); err != nil {
		t.Fatal(err)
	}
	set := Settings{Threshold: 75, StateMaxAge: 120, HandoffMaxBlks: 2}
	stop := DecideStop(StopIn{StopHookActive: true}, st, set, keepstate.Handoff{}, "/h", t0)
	guard := DecideGuardStop(st, set, keepstate.Handoff{}, t0)
	if stop.Block || stop.Persist || guard.Block || guard.Persist || !st.OrchestratorHandoff.HandoffFailed || !st.GuardHandoff.HandoffFailed {
		t.Fatalf("legacy failure latch lost: stop=%+v guard=%+v", stop, guard)
	}
}
