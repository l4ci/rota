package milestone

import (
	"reflect"
	"strings"
	"testing"
)

func TestChildrenSectionRoundTrip(t *testing.T) {
	plan := "---\nid: M01\n---\n\n# Plan\n\ntext"
	sec := RenderChildren([]ChildRef{{Ref: "#3", Title: "first"}, {Ref: "#9", Title: "second"}})
	body := JoinChildren(plan, sec)
	gotPlan, gotSec, ok := SplitChildren(body)
	if !ok || gotPlan != plan || gotSec != sec {
		t.Fatalf("split = %q %q %v", gotPlan, gotSec, ok)
	}
	if want := []string{"#3", "#9"}; !reflect.DeepEqual(ChildRefs(sec), want) {
		t.Errorf("refs = %v", ChildRefs(sec))
	}
	if !strings.Contains(sec, "- #3 first\n- #9 second") {
		t.Errorf("section = %q", sec)
	}
	if _, _, ok := SplitChildren(plan); ok {
		t.Error("section found in a plan without one")
	}
	if got := JoinChildren(plan, ""); got != plan {
		t.Errorf("join with no section = %q", got)
	}
}
