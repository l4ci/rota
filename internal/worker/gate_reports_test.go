package worker

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/gittest"

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
			res, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main", CheckOnly: true, ReviewsOnly: tc.reviewsOnly, CheckReviews: true})
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

// In an umbrella the store is keyed "<repo>:<branch>": the reports the gate
// recovers must land under that key, where BlockGate and the review-depth
// check read them.
func TestGateRecoversPRVerdictsUnderUmbrellaKey(t *testing.T) {
	for _, tc := range []struct {
		name, v string
		want    string
	}{
		{"FAIL blocks", verdict.Fail, GateVerdictBlocked},
		{"PASS satisfies depth", verdict.Pass, GateFresh},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t, "7")
			w.setConfig(`{"ship":{"review":"full"},"test":{"full":["true"]}}`)
			head := w.forgeWord("sha")
			var notes []tracker.Comment
			for _, kind := range []string{verdict.ReviewSpec, verdict.ReviewQuality} {
				notes = append(notes, tracker.Comment{Author: "reviewer", Body: verdict.RenderReport(verdict.Record{Kind: kind, Verdict: tc.v, Sha: head}, nil)})
			}
			e := w.env(false)
			e.Forge = func(provider, dir string, _ tracker.Settings) (Forge, error) {
				return &verdictReportForge{fakeForge: &fakeForge{w: w, provider: provider}, notes: notes}, nil
			}
			res, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main", CheckOnly: true, CheckReviews: true, VerdictRepo: "sub"})
			if res.Verdict != tc.want {
				t.Fatalf("want %s: %+v err=%v", tc.want, res, err)
			}
			if tc.v == verdict.Fail && err == nil {
				t.Fatal("FAIL not returned")
			}
			store := verdict.Load(w.dir)
			if len(store.Branches[verdict.BranchKey("sub", "w1")]) != 2 || len(store.Branches) != 1 {
				t.Fatalf("recovered reports must be the two records under the sub-repo key only: %v", store.Branches)
			}
		})
	}
}

// Observe is the preview's readiness probe: it never fetches and never writes
// the verdict store, so a PASS that lives only as a PR report is not recovered
// (execution recovers it), and the remote refs are judged as last fetched.
func TestGateObserveReadsWithoutFetchOrRecovery(t *testing.T) {
	w := newWorld(t, "7")
	w.setConfig(`{"ship":{"review":"full"},"test":{"full":["true"]}}`)
	head := w.forgeWord("sha")
	var notes []tracker.Comment
	for _, kind := range []string{verdict.ReviewSpec, verdict.ReviewQuality} {
		notes = append(notes, tracker.Comment{Author: "reviewer", Body: verdict.RenderReport(verdict.Record{Kind: kind, Verdict: verdict.Pass, Sha: head}, nil)})
	}
	gittest.Run(t, w.dir, "fetch", "-q", "origin")
	e := w.env(false)
	e.Forge = func(provider, dir string, _ tracker.Settings) (Forge, error) {
		return &verdictReportForge{fakeForge: &fakeForge{w: w, provider: provider}, notes: notes}, nil
	}
	var calls []string
	e.Git = func(ctx context.Context, dir string, args ...string) (git.Result, error) {
		calls = append(calls, strings.Join(args, " "))
		return git.Exec(ctx, dir, args...)
	}
	res, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main", Observe: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range calls {
		if strings.HasPrefix(c, "fetch") {
			t.Errorf("observation fetched: %q", c)
		}
	}
	if _, statErr := os.Stat(verdict.Path(w.dir)); !os.IsNotExist(statErr) {
		t.Errorf("observation wrote the verdict store: %v", statErr)
	}
	if res.Verdict != GateReviewMissing {
		t.Fatalf("local verdicts only: want %s, got %+v", GateReviewMissing, res)
	}
}

// One recovered FAIL must reach every reader the umbrella key feeds: the gate
// refuses on it, and the bounce count and the review relay read the same list
// (noBlockingFindings reads BounceReviewsAtHead, failVerdict reads EffectiveFail).
func TestRecoveredFailReachesGateBounceAndRelay(t *testing.T) {
	w := newWorld(t, "7")
	w.setConfig(`{"ship":{"review":"full"},"test":{"full":["true"]}}`)
	head := w.forgeWord("sha")
	var notes []tracker.Comment
	for _, kind := range []string{verdict.ReviewSpec, verdict.ReviewQuality} {
		notes = append(notes, tracker.Comment{Author: "reviewer", Body: verdict.RenderReport(verdict.Record{Kind: kind, Verdict: verdict.Fail, Sha: head}, nil)})
	}
	e := w.env(false)
	e.Forge = func(provider, dir string, _ tracker.Settings) (Forge, error) {
		return &verdictReportForge{fakeForge: &fakeForge{w: w, provider: provider}, notes: notes}, nil
	}
	res, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main", CheckOnly: true, CheckReviews: true, VerdictRepo: "sub"})
	if res.Verdict != GateVerdictBlocked || err == nil {
		t.Fatalf("gate must refuse on the recovered FAIL: %+v err=%v", res, err)
	}
	store := verdict.Load(w.dir)
	list := store.Branch("sub", "w1")
	if got := verdict.BounceReviewsAtHead(list, head); len(got) == 0 || !got[0].Failing() {
		t.Fatalf("bounce reader sees no failing review at head: %+v", got)
	}
	if _, ok := verdict.EffectiveFail(list); !ok {
		t.Fatal("relay reader sees no effective FAIL")
	}
	if len(store.Branch("", "w1")) != 0 {
		t.Fatal("the plain key must stay empty in an umbrella")
	}
}
