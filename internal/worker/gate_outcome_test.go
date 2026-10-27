package worker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/tracker"
)

// setOutcomeSlot writes slot w1 holding PR with a recorded done outcome ("" for none).
func (w *world) setOutcomeSlot(outcome string) {
	extra := ""
	if outcome != "" {
		extra = fmt.Sprintf(`,"outcome":{"kind":%q,"summary":["a","b","c"]}`, outcome)
	}
	os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"),
		[]byte(fmt.Sprintf(`{"slots":[{"name":"w1","branch":"w1","task":"#5","pr":%q%s}]}`, ghURL, extra)), 0o644)
	w.setPRBody("Closes #5\n")
}

// A PR whose worker reported partial or failed goes to review or relay first:
// the gate refuses it, under --check-only too. A done outcome, or none, passes.
func TestGateRefusesAPartialOrFailedOutcome(t *testing.T) {
	cases := []struct {
		outcome string
		check   bool
		want    string
	}{
		{"partial", false, GateOutcomeOpen},
		{"failed", false, GateOutcomeOpen},
		{"partial", true, GateOutcomeOpen},
		{"done", false, GatePass},
		{"", false, GatePass},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%s check=%v", c.outcome, c.check), func(t *testing.T) {
			w := newWorld(t, ghURL)
			w.pushStamped("a.txt", "")
			w.setOutcomeSlot(c.outcome)
			res, err := w.gate(false, GateOpts{CheckOnly: c.check})
			if err != nil || res.Verdict != c.want {
				t.Fatalf("verdict = %q (%v), want %q: %+v", res.Verdict, err, c.want, res)
			}
			if c.want != GateOutcomeOpen {
				return
			}
			if res.Changed || w.onMain("a.txt") {
				t.Errorf("nothing may land: %+v", res)
			}
			if !strings.Contains(res.Err, c.outcome) {
				t.Errorf("message names the outcome: %q", res.Err)
			}
		})
	}
}

// A new brief (a relay or a transfer) answers the outcome: it no longer gates.
func TestDispatchClearsTheOutcome(t *testing.T) {
	for _, task := range []string{"", "#9"} {
		s := NewSlot("w1", "w1", "", "", "")
		s.SetOutcome(OutcomePartial, []string{"a", "b", "c"}, "2026-10-20T00:00:00Z")
		s.Dispatch("h", task, "", "2026-10-20T01:00:00Z")
		if k, _ := s.Outcome(); k != "" {
			t.Errorf("task %q: outcome %q survived a dispatch", task, k)
		}
	}
}

// A slice PR on a partial-slice issue reports `done` (this PR's slice is
// complete), says `Refs #N`, and lands without a relay. Reporting `partial`
// there stays refused: that outcome is for unmet criteria in the PR itself.
func TestGateLandsADoneSlicePROnAPartialSliceIssue(t *testing.T) {
	for _, c := range []struct {
		outcome string
		verdict string
	}{
		{"done", GatePass},
		{"partial", GateOutcomeOpen},
	} {
		t.Run(c.outcome, func(t *testing.T) {
			w := newWorld(t, ghURL)
			w.pushStamped("a.txt", "")
			w.setConfig(`{"issues":{"source":"gitlab"},"test":{"full":["true"]}}`)
			w.setOutcomeSlot(c.outcome)
			w.setPRBody("Refs #5\n")
			e := w.env(false)
			e.Issues = func(string, tracker.Settings) (Issues, error) {
				return &issueStub{labels: []string{PartialSliceLabel}}, nil
			}
			res, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main"})
			if err != nil || res.Verdict != c.verdict {
				t.Fatalf("verdict = %q (%v), want %q: %+v", res.Verdict, err, c.verdict, res)
			}
		})
	}
}
