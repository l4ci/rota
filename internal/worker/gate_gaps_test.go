package worker

import (
	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/verdict"
	"strings"
	"testing"
)

// gapKinds is the blockedBy keys of a refusal's gaps, in order.
func gapKinds(r GateResult) []string {
	var out []string
	for _, g := range r.Gaps {
		out = append(out, g.BlockedBy)
	}
	return out
}

// A PR with an open outcome and a missing ## Assumptions/## Rulings gets one
// refusal naming both. The verdict stays the first gap in check order.
func TestGateListsEveryRecordGap(t *testing.T) {
	for _, tc := range []struct {
		name      string
		outcome   string
		body      string
		wantKinds []string
	}{
		{"two gaps", "partial", "Closes #5\n", []string{GateOutcomeOpen, GateAssumptionsMissing}},
		{"three gaps", "failed", "Closes #5\n\n## Approvals\n- orchestrator relay round 9: go\n", []string{GateOutcomeOpen, GateProvenanceFail, GateAssumptionsMissing}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t, ghURL)
			w.pushStamped("a.txt", "")
			w.setOutcomeSlot(tc.outcome)
			w.forge("body", tc.body)
			res, err := w.gate(false, GateOpts{CheckOnly: true})
			if err != nil || res.Changed {
				t.Fatalf("%+v %v", res, err)
			}
			if res.Verdict != tc.wantKinds[0] {
				t.Errorf("verdict must stay the first gap %q, got %q", tc.wantKinds[0], res.Verdict)
			}
			if got := gapKinds(res); strings.Join(got, ",") != strings.Join(tc.wantKinds, ",") {
				t.Fatalf("gaps = %v, want %v: %+v", got, tc.wantKinds, res)
			}
			for _, g := range res.Gaps {
				if g.Msg == "" || g.Hint == "" {
					t.Errorf("each gap carries its own message and fix hint: %+v", g)
				}
				if !strings.Contains(res.Err, g.Msg) || !strings.Contains(res.Err, g.Hint) {
					t.Errorf("the refusal text must carry gap %s: %q", g.BlockedBy, res.Err)
				}
			}
		})
	}
}

// One gap reads as it did before: the same message, no list.
func TestGateSingleGapUnchanged(t *testing.T) {
	w := newWorld(t, ghURL)
	w.pushStamped("a.txt", "")
	w.setOutcomeSlot("")
	w.forge("body", "Closes #5\n")
	res, err := w.gate(false, GateOpts{CheckOnly: true})
	if err != nil || res.Verdict != GateAssumptionsMissing {
		t.Fatalf("%+v %v", res, err)
	}
	if len(res.Gaps) > 1 || strings.Contains(res.Err, "also") {
		t.Errorf("one gap must stay unlisted: %+v", res)
	}
	if !strings.Contains(res.Err, "is missing ## Assumptions and ## Rulings; nothing landed") {
		t.Errorf("message changed: %q", res.Err)
	}
}

// A train member the full check refuses for two record gaps reports both in
// the train's error, with the first gap's verdict.
func TestTrainMemberListsEveryGap(t *testing.T) {
	w, e := sectionTrainWorld(t, []string{completePRSections, "## Approvals\n- orchestrator relay round 9: go\n" + completePRSections})
	w.setConfig(`{"ship":{"review":"light"},"test":{"full":["true"]}}`)
	w.record("w1", gittest.Run(t, w.worker, "rev-parse", "w1"), verdict.ReviewQuality)
	res, err := e.Train(bg, w.dir, TrainOpts{Base: "main", Targets: []string{"w1", "w2"}, CheckReviews: true})
	if err != nil || res.Verdict != GateProvenanceFail || res.Culprit != "w2" {
		t.Fatalf("%+v %v", res, err)
	}
	if got := gapKinds(res.CulpritGate); strings.Join(got, ",") != GateProvenanceFail+","+GateReviewMissing {
		t.Fatalf("gaps = %v: %+v", got, res.CulpritGate)
	}
	if !strings.Contains(res.Err, "review-quality verdict") || !strings.Contains(res.Err, "orchestrator relay round 9") {
		t.Errorf("train error must carry every gap: %q", res.Err)
	}
}
