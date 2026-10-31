package limits

import "testing"

func TestEntryRoundTrip(t *testing.T) {
	root := t.TempDir()
	lim := Entry{ID: "l1", Session: "kit", Window: "five_hour", ResetsAt: "2026-10-03T15:00:00Z", Cycles: 2, Status: StatusWaiting}
	if err := Update(root, func(l []Entry) []Entry { return append(l, lim) }); err != nil {
		t.Fatal(err)
	}
	if got := Load(root); len(got) != 1 || got[0] != lim {
		t.Errorf("limits = %+v, want [%+v]", got, lim)
	}
}
