package backlog

import "testing"

// plainTracker is a Tracker with none of the optional capabilities.
type plainTracker struct{ Tracker }

func TestCapabilityRefusals(t *testing.T) {
	b := &Issues{Tracker: plainTracker{}}
	if _, err := capability[PRTracker](b, noPRSupport); err == nil || err.Error() != noPRSupport {
		t.Fatalf("PRTracker: err = %v, want %q", err, noPRSupport)
	}
	if _, err := b.milestoneTracker(); err == nil || err.Error() != noMilestoneSupport {
		t.Fatalf("milestoneTracker: err = %v, want %q", err, noMilestoneSupport)
	}
	if _, err := capability[MilestoneCreator](&Issues{}, "x"); err == nil || err.Error() != "issues backend has no tracker" {
		t.Fatalf("no tracker: err = %v", err)
	}
}

func TestViaOwnerUnknownRefIsNotFound(t *testing.T) {
	u := &Umbrella{}
	_, err := viaOwner(u, "F99", func(*Issues, string) (int, error) { t.Fatal("call ran"); return 0, nil })
	if err == nil {
		t.Fatal("want ErrNotFound for a reference no sub-repo holds")
	}
}
