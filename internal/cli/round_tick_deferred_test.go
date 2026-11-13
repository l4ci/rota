package cli

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/roundtick"
)

// A deferred assign shows in both the text lines and the JSON data.
func TestTickReportsDeferred(t *testing.T) {
	r := roundtick.Result{Deferred: "gate running, deferred: held for #1900"}
	if got := tickLines(r); !strings.Contains(got, "deferred\tgate running, deferred: held for #1900") {
		t.Errorf("tickLines = %q, want a deferred line", got)
	}
	if got, _ := tickData(r).Get("deferred"); got != r.Deferred {
		t.Errorf("tickData deferred = %v, want %q", got, r.Deferred)
	}
}

// A held due PR names its reason in the text lines and the JSON data.
func TestTickReportsHolds(t *testing.T) {
	r := roundtick.Result{Holds: []roundtick.Item{{Kind: "train-hold", Target: "#2", Why: "over round.autopilotCap (1) merges this tick; next tick"}}}
	if got := tickLines(r); !strings.Contains(got, "hold\t#2\tover round.autopilotCap") {
		t.Errorf("tickLines = %q, want a hold line", got)
	}
	if _, ok := tickData(r).Get("holds"); !ok {
		t.Error("tickData has no holds")
	}
}
