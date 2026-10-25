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
