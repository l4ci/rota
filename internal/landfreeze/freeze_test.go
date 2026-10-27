package landfreeze

import "testing"

func TestRepairPRReferences(t *testing.T) {
	for _, tc := range []struct {
		repair, pr string
		allowed    bool
	}{
		{"7", "https://github.com/o/r/pull/7", true},
		{"#7", "https://github.com/o/r/pull/7", true},
		{"007", "https://github.com/o/r/pull/7", true},
		{"https://github.com/o/r/pull/7?diff=split", "https://github.com/o/r/pull/7", true},
		{"https://github.com/other/repo/pull/7", "https://github.com/o/r/pull/7", false},
		{"7", "https://github.com/o/r/pull/8", false},
	} {
		t.Run(tc.repair, func(t *testing.T) {
			r := &Record{Active: true, RepairPR: tc.repair}
			if got := r.Allows("", tc.pr); got != tc.allowed {
				t.Fatalf("repair=%q PR=%q: allows=%v want %v", tc.repair, tc.pr, got, tc.allowed)
			}
		})
	}
}
