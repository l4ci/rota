package worker

import "testing"

func TestRouteBaseRemembersAndRestoresTheRegisteredBase(t *testing.T) {
	s := NewSlot("ben", "park/ben", "/wt", "main", "")
	s.RouteBase("milestone/M01")
	if s.Base() != "milestone/M01" {
		t.Fatalf("base = %q", s.Base())
	}
	s.RouteBase("milestone/M02") // re-routed: the home base must survive
	s.RouteBase("")
	if s.Base() != "main" {
		t.Errorf("base after restore = %q, want main", s.Base())
	}
	s.RouteBase("") // nothing routed: nothing changes
	if s.Base() != "main" {
		t.Errorf("base = %q", s.Base())
	}
}
