package round

import (
	"reflect"
	"testing"
)

// NotReady owns the order of the reasons a candidate is not ready, so every
// view of a candidate says the same thing.
func TestCandidateNotReady(t *testing.T) {
	failed := Readiness{
		Checks:   []Check{{Name: "deps", OK: false, Detail: []string{"waits on #4", "waits on #5"}}, {Name: "files", OK: true}, {Name: "labels", OK: false}},
		Overlaps: []Overlap{{With: "#9", Slot: "ben", Scopes: []string{"post /items"}}},
	}
	cases := []struct {
		name string
		c    Candidate
		want []string
	}{
		{"ready", Candidate{Readiness: Readiness{Checks: []Check{{Name: "deps", OK: true}}}}, nil},
		{"ready with a warn-only overlap", Candidate{Readiness: Readiness{Overlaps: []Overlap{{With: "9"}}}}, nil},
		{"open PR", Candidate{OpenPR: 7}, []string{"waiting on open PR #7"}},
		{"dependency PR", Candidate{DependsOnPR: 8}, []string{"depends on PR #8"}},
		{"pick refused", Candidate{PickErr: "unknown harness"}, []string{"unknown harness"}},
		{"blocked by", Candidate{BlockedBy: "needs a decision"}, []string{"needs a decision"}},
		{"every reason, in order", Candidate{OpenPR: 7, DependsOnPR: 8, PickErr: "bad pick", BlockedBy: "needs a decision", Readiness: failed},
			[]string{"waiting on open PR #7", "depends on PR #8", "overlaps #9 (ben): scopes post /items", "deps: waits on #4; waits on #5", "labels", "bad pick", "needs a decision"}},
	}
	for _, tc := range cases {
		if got := tc.c.NotReady(); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: NotReady = %q, want %q", tc.name, got, tc.want)
		}
	}
}
