package cycle

import "testing"

func TestGate(t *testing.T) {
	for _, tc := range []struct {
		name   string
		in     Signals
		action string
		reason string
	}{
		{"two items", Signals{Items: 2}, "fire", "2 items"},
		{"five files", Signals{Files: 5}, "fire", "5 files"},
		{"two debug cycles", Signals{DebugCycles: 2}, "fire", "2 debug cycles"},
		{"several fire", Signals{Items: 3, Files: 9}, "fire", "3 items, 9 files"},
		{"just below", Signals{Items: 1, Files: 4}, "skip", "1 item, 4 files, 0 debug cycles"},
		{"quiet", Signals{Items: 1, Files: 3}, "skip", "1 item, 3 files, 0 debug cycles"},
		{"one debug cycle", Signals{DebugCycles: 1}, "skip", "0 items, 0 files, 1 debug cycle"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Gate(tc.in)
			if got.Action != tc.action || got.Reason != tc.reason {
				t.Errorf("Gate(%+v) = %+v, want %s %q", tc.in, got, tc.action, tc.reason)
			}
		})
	}
}
