package worker

import (
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
)

func TestHeldID(t *testing.T) {
	for _, c := range []struct{ task, branch, name, want string }{
		{"58", "x/1-y", "x", "58"}, {"#58", "", "x", "58"}, {"b07", "omar/58-a", "omar", "B07"},
		{"", "omar/58-a", "omar", "58"}, {"", "park/omar", "omar", ""}, {"", "main", "omar", ""},
	} {
		if got := HeldID(c.task, c.branch, c.name); got != c.want {
			t.Errorf("HeldID(%q,%q,%q) = %q, want %q", c.task, c.branch, c.name, got, c.want)
		}
	}
}

func TestSlotHeldID(t *testing.T) {
	o := jsonx.NewObject()
	o.Set("name", "omar")
	o.Set("task", "#12")
	o.Set("branch", "park/omar")
	s := AsSlot(o)
	if got := s.HeldID(); got != "12" {
		t.Errorf("HeldID = %q, want 12", got)
	}
}

func TestPRRefNumber(t *testing.T) {
	for in, want := range map[string]int{"https://github.com/o/r/pull/9": 9, "#12": 12, "7": 7, "https://gitlab.com/o/r/-/merge_requests/3": 3, "omar/58": 0, "omar/58-x": 0} {
		if n, _ := PRRefNumber(in); n != want {
			t.Errorf("PRRefNumber(%q) = %d, want %d", in, n, want)
		}
	}
}

func TestBranchIssue(t *testing.T) {
	if n, ok := BranchIssue("nia/288-x"); !ok || n != 288 {
		t.Errorf("BranchIssue = %d,%v", n, ok)
	}
	if _, ok := BranchIssue("park/nia"); ok {
		t.Error("park branch holds no issue")
	}
}

// The recorded states that wait on a human, and the one place the two
// consumers differ: done wakes `round wait` but never needs the autopilot's
// human.
func TestAttentionSets(t *testing.T) {
	for _, st := range []string{"blocked", "needs-permission", "limited", "dead", "unknown", "BLOCKED"} {
		if !NeedsAttention(st) || !NeedsWake(st) {
			t.Errorf("%s should need attention and wake", st)
		}
	}
	if NeedsAttention("done") || !NeedsWake("done") {
		t.Error("done wakes the watch but is not an attention state")
	}
	for _, st := range []string{"busy", "idle", ""} {
		if NeedsAttention(st) || NeedsWake(st) {
			t.Errorf("%s should need nothing", st)
		}
	}
}
