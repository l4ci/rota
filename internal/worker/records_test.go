package worker

import "testing"

func TestTypedRowsRoundTrip(t *testing.T) {
	root := t.TempDir()
	esc := Escalation{ID: "e1", Kind: "pr", Number: 3, Slot: "dana", Title: "q", CommentID: "9",
		SentAt: "2026-10-03T10:00:00Z", Notified: true, Status: EscalationPending}
	lim := Limit{ID: "l1", Session: "kit", Window: "five_hour", ResetsAt: "2026-10-03T15:00:00Z", Cycles: 2, Status: "waiting"}
	if err := UpdateEscalations(root, func(l []Escalation) []Escalation { return append(l, esc) }); err != nil {
		t.Fatal(err)
	}
	if err := UpdateLimits(root, func(l []Limit) []Limit { return append(l, lim) }); err != nil {
		t.Fatal(err)
	}
	reg := LoadRegistry(root)
	if got := reg.Escalations(); len(got) != 1 || got[0] != esc {
		t.Errorf("escalations = %+v, want [%+v]", got, esc)
	}
	if got := reg.Limits(); len(got) != 1 || got[0] != lim {
		t.Errorf("limits = %+v, want [%+v]", got, lim)
	}
}
