package worker

import (
	"context"
	"fmt"
	"testing"

	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/verdict"
)

type verdictReportForge struct {
	*fakeForge
	notes []tracker.Comment
}

func (f *verdictReportForge) MRNotes(context.Context, int) ([]tracker.Comment, error) {
	return f.notes, nil
}
func (f *verdictReportForge) AuthenticatedUser(context.Context) (string, error) {
	return "reviewer", nil
}

func TestGateRecoversPRVerdicts(t *testing.T) {
	for _, tc := range []struct {
		name, v     string
		reviewsOnly bool
		want        string
	}{
		{"FAIL blocks", verdict.Fail, false, GateVerdictBlocked},
		{"PASS satisfies depth", verdict.Pass, false, GateFresh},
		{"review probe recovers PASS", verdict.Pass, true, GateFresh},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t, "7")
			w.setConfig(`{"ship":{"review":"full"},"test":{"full":["true"]}}`)
			head := w.forgeWord("sha")
			notes := []tracker.Comment{}
			for _, kind := range []string{verdict.ReviewSpec, verdict.ReviewQuality} {
				notes = append(notes, tracker.Comment{Author: "reviewer", Body: verdict.RenderReport(verdict.Record{Kind: kind, Verdict: tc.v, Sha: head}, nil)})
			}
			e := w.env(false)
			e.Forge = func(provider, dir string, _ tracker.Settings) (Forge, error) {
				return &verdictReportForge{fakeForge: &fakeForge{w: w, provider: provider}, notes: notes}, nil
			}
			block := func(branch, head string) error {
				if r, ok := verdict.Blocking(verdict.Load(w.dir).Branches[branch], verdict.Settings{}); ok {
					return fmt.Errorf("%s remote FAIL", r.Kind)
				}
				return nil
			}
			res, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main", CheckOnly: true, ReviewsOnly: tc.reviewsOnly, CheckReviews: true, Verdict: block})
			if res.Verdict != tc.want {
				t.Fatalf("want %s: %+v err=%v", tc.want, res, err)
			}
			if tc.v == verdict.Fail && err == nil {
				t.Fatal("FAIL not returned")
			}
			if w.onMain("work.txt") {
				t.Fatal("check-only merged")
			}
		})
	}
}
