package cli

import "testing"

// With no resolvable default base, an empty-base PR keeps its own group: it does
// not join a PR that recorded a base. The nudge degrades to under-reporting.
func TestTrainNudgeUnresolvedDefaultKeepsEmptyBaseApart(t *testing.T) {
	w := fillDefaultBase([]waitingPR{
		{PR: "#9", Base: "", Reviewed: true},
		{PR: "#10", Base: "main", Reviewed: true},
	}, "")
	if ns := trainNudges(w); len(ns) != 0 {
		t.Fatalf("nudges = %+v, want none: empty base stays apart from main", ns)
	}
}
