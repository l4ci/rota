package ledger

import (
	"testing"
	"time"
)

func TestLatestAssignByIssue(t *testing.T) {
	entries := []Entry{
		{Kind: KindAssign, Issue: "12", Slot: "ben", Account: "old", TS: rowTS.Add(time.Hour)},
		{Kind: KindAssign, Issue: "12", Slot: "dana", Account: "latest", TS: rowTS},
		{Kind: KindDone, Issue: "12", Slot: "dana", Account: "done"},
		{Kind: KindAssign, Issue: "13", Slot: "dana", Account: "other issue"},
	}
	got := LatestAssign(entries, AssignQuery{Issue: "12"})
	if got == nil || got.Account != "latest" || got.Slot != "dana" {
		t.Fatalf("LatestAssign = %+v, want latest assignment to dana in file order", got)
	}
}

func TestLatestAssignFilters(t *testing.T) {
	entries := []Entry{
		{Kind: KindAssign, Issue: " #b07 ", Slot: "ben", Account: "old", Harness: "claude"},
		{Kind: KindAssign, Issue: "B07", Slot: "ben", Account: "ben", Harness: "codex"},
		{Kind: KindAssign, Issue: "b07", Slot: "dana", Account: "dana", Harness: "claude"},
		{Kind: KindAssign, Issue: "#B07", Slot: "ben", Account: "unknown harness"},
		{Kind: KindDone, Issue: "B07", Slot: "ben", Account: "done", Harness: "claude"},
		{Kind: KindAssign, Issue: "B08", Slot: "ben", Account: "other issue", Harness: "claude"},
		{Kind: KindAssign, Issue: " #proj-12 ", Slot: "kit", Account: "normalised stored issue"},
		{Kind: KindAssign, Issue: "", Slot: "ben", Account: "no issue"},
	}
	for _, tc := range []struct {
		name string
		q    AssignQuery
		want string
	}{
		{"slot and normalised issue", AssignQuery{Issue: " #b07 ", Slot: "ben"}, "unknown harness"},
		{"other slot", AssignQuery{Issue: "B07", Slot: "dana"}, "dana"},
		{"known harness on slot", AssignQuery{Issue: "B07", Slot: "ben", RequireHarness: true}, "ben"},
		{"stored issue normalised", AssignQuery{Issue: "PROJ-12"}, "normalised stored issue"},
		{"no known harness", AssignQuery{Issue: "PROJ-12", RequireHarness: true}, ""},
		{"known harness any slot", AssignQuery{Issue: "b07", RequireHarness: true}, "dana"},
		{"absent issue", AssignQuery{Issue: "B09"}, ""},
		{"absent slot", AssignQuery{Issue: "B07", Slot: "kit"}, ""},
		{"slot is exact", AssignQuery{Issue: "B07", Slot: "Ben"}, ""},
		{"empty issue", AssignQuery{}, ""},
		{"normalised empty issue", AssignQuery{Issue: " # "}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := LatestAssign(entries, tc.q)
			if tc.want == "" {
				if got != nil {
					t.Fatalf("LatestAssign = %+v, want nil", got)
				}
			} else if got == nil || got.Account != tc.want {
				t.Fatalf("LatestAssign = %+v, want account %q", got, tc.want)
			}
		})
	}
	if got := LatestAssign(nil, AssignQuery{Issue: "B07"}); got != nil {
		t.Fatalf("LatestAssign with no entries = %+v, want nil", got)
	}
}
