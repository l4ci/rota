package worker

import "testing"

// TestClassifyBlockedBeforeRelayIsNotBlocked pins #1096: a relay that answers a
// ROTA-BLOCKED question lands under that line, which stays in the pane. The
// question predates the relay, so the slot is not blocked until the worker
// prints a blocked line after it.
func TestClassifyBlockedBeforeRelayIsNotBlocked(t *testing.T) {
	relay := "--- ORCHESTRATOR (round 29) ---\n[ORCHESTRATOR RELAY — forwarded.]\n[relay 1]\n\nUse option 1.\n\n"
	pane := "● ROTA-BLOCKED ben: which option?\n\n" + relay
	for i := 0; i < 2; i++ { // two watch ticks over the same scrollback
		if st, ev := Classify(pane, false, 60, "idle"); st == StateBlocked {
			t.Fatalf("tick %d: answered question read as blocked: %s %q", i, st, ev)
		}
	}
	// A different question after the relay is reported.
	other := pane + "● thinking\n● ROTA-BLOCKED ben: another question\n"
	if st, ev := Classify(other, false, 60, "idle"); st != StateBlocked || ev != "another question" {
		t.Errorf("new question: %s %q", st, ev)
	}
	// The worker re-asking the same question after the relay is blocked again.
	again := pane + "● ROTA-BLOCKED ben: which option?\n"
	if st, ev := Classify(again, false, 60, "idle"); st != StateBlocked || ev != "which option?" {
		t.Errorf("re-asked question: %s %q", st, ev)
	}
}

// TestDispatchRelayAnswersBlockedSlot pins #1096: a relay to a blocked slot
// drops the blocked event and marks the slot busy.
func TestDispatchRelayAnswersBlockedSlot(t *testing.T) {
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	f := tmuxFake()
	e := envWith(f)
	if _, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "task\n"), Task: "T1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateSlot(dir, "w1", func(s *Slot) {
		s.SetEvent(EventBlocked, "which option?", "2026-10-02T15:00:00Z")
		s.MarkState(StateBlocked, "2026-10-02T15:00:00Z")
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "use 1\n"), Relay: true}); err != nil {
		t.Fatal(err)
	}
	s := LoadRegistryTolerant(dir).Slot("w1")
	if s.State() != "busy" || s.Event() != nil {
		t.Errorf("after the relay: state %q, event %+v", s.State(), s.Event())
	}
}
