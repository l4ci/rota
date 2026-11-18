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

// A record gap stays the refusal's first verdict when a later check refuses
// too (gapsFirst): the gate used to stop at the gap, so the later refusal
// never showed. Only a gap recorded before the later check can pair with it:
// outcome-open runs ahead of staleness and the PR match, provenance only ahead
// of the verdict check.
func TestGateKeepsFirstGapAheadOfALaterRefusal(t *testing.T) {
	const badRelay = "Closes #5\n\n## Approvals\n- orchestrator relay round 9: go\n"
	failVerdict := func(w *world) {
		if _, err := verdict.AddBranch(w.dir, "w1", verdict.Record{Kind: verdict.ReviewSpec, Verdict: verdict.Fail, Sha: "abc1234"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name    string
		outcome string
		body    string
		setup   func(*world)
		want    string // the first gap, which must stay the verdict
		later   string // the refusal that would have won without gapsFirst
	}{
		{"outcome-open before stale", "partial", "Closes #5\n", func(w *world) { advanceMainOn(w, "work.txt") }, GateOutcomeOpen, GateStale},
		{"outcome-open before pr-mismatch", "partial", "Closes #5\n", func(w *world) { w.forge("sha", strings.Repeat("0", 40)) }, GateOutcomeOpen, GatePRMismatch},
		{"outcome-open before verdict-blocked", "partial", "Closes #5\n", failVerdict, GateOutcomeOpen, GateVerdictBlocked},
		{"provenance before verdict-blocked", "", badRelay, failVerdict, GateProvenanceFail, GateVerdictBlocked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t, ghURL)
			w.pushStamped("a.txt", "")
			w.setOutcomeSlot(tc.outcome)
			w.forge("body", tc.body)
			tc.setup(w)
			res, err := w.gate(false, GateOpts{CheckOnly: true})
			if err != nil || res.Changed {
				t.Fatalf("a record gap is a refusal, not an error: %+v %v", res, err)
			}
			if res.Verdict != tc.want {
				t.Fatalf("verdict = %q, want the first gap %q (not %q): %+v", res.Verdict, tc.want, tc.later, res)
			}
		})
	}
}
