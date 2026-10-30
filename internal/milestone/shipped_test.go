package milestone

import "testing"

func TestClosedItemBullet(t *testing.T) {
	for _, tc := range []struct{ ref, title, reason, want string }{
		{"B07", "Fix it", "done", "- B07: Fix it (done)"},
		{"#12", "Fix it", "", "- #12: Fix it"},
		{"PROJ-3", "Fix it", "", "- PROJ-3: Fix it"},
	} {
		if got := ClosedItemBullet(tc.ref, tc.title, tc.reason); got != tc.want {
			t.Errorf("ClosedItemBullet(%q,%q,%q) = %q, want %q", tc.ref, tc.title, tc.reason, got, tc.want)
		}
	}
}
