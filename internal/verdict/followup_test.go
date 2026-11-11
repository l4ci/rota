package verdict

import (
	"strings"
	"testing"
)

func TestDeferredAbove(t *testing.T) {
	fs := []Finding{
		{Severity: "blocker", Title: "b"}, {Severity: "major", Title: "m"},
		{Severity: "minor", Title: "n"}, {Severity: "info", Title: "i"},
	}
	titles := func(floor string) string {
		var out []string
		for _, f := range DeferredAbove(fs, floor) {
			out = append(out, f.Title)
		}
		return strings.Join(out, ",")
	}
	if got := titles(FloorMinor); got != "n" {
		t.Errorf("minor floor kept %q", got)
	}
	if got := titles(FloorOff); got != "" {
		t.Errorf("off floor kept %q", got)
	}
}

func TestDeferredAboveMajorFloor(t *testing.T) {
	fs := []Finding{{Severity: "major", Title: "m", Blocking: new(bool)}, {Severity: "minor", Title: "n"}}
	got := DeferredAbove(fs, FloorMajor)
	if len(got) != 1 || got[0].Title != "m" {
		t.Errorf("major floor kept %v", got)
	}
}

// The deferred line is one contract: what DeferredLine writes, IsDeferredFinding reads.
func TestDeferredLineRoundTrip(t *testing.T) {
	line := DeferredLine("minor", "review-spec", "FAIL", "#9")
	if !IsDeferredFinding("## Problem\n\nx\n\n" + line + "\n\n## Acceptance\n") {
		t.Errorf("recogniser rejects its own line %q", line)
	}
	if IsDeferredFinding("Related to a verdict on #9.\n") {
		t.Error("recogniser accepts an unrelated body")
	}
}
