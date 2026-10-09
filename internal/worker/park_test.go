package worker

import "testing"

func TestParkBranchRule(t *testing.T) {
	if got := ParkBranch("ben"); got != "park/ben" {
		t.Fatalf("ParkBranch = %q", got)
	}
	for b, want := range map[string]bool{"park/ben": true, "park/": true, "ben/1-x": false, "main": false, "": false, "xpark/ben": false} {
		if IsPark(b) != want {
			t.Errorf("IsPark(%q) = %v, want %v", b, !want, want)
		}
	}
	for _, c := range []struct {
		b, n string
		want bool
	}{{"park/ben", "ben", true}, {"park/dana", "ben", false}, {"ben/1-x", "ben", false}, {"", "ben", false}} {
		if IsOwnPark(c.b, c.n) != c.want {
			t.Errorf("IsOwnPark(%q,%q) = %v, want %v", c.b, c.n, !c.want, c.want)
		}
	}
}
