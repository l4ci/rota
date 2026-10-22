package worker

import "testing"

func TestOutcomeOpen(t *testing.T) {
	for _, tc := range []struct {
		kind string
		want bool
	}{
		{OutcomeDone, false},
		{OutcomePartial, true},
		{OutcomeFailed, true},
		{"", false},
	} {
		if got := OutcomeOpen(tc.kind); got != tc.want {
			t.Errorf("OutcomeOpen(%q) = %v, want %v", tc.kind, got, tc.want)
		}
	}
}
