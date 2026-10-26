package cli

import (
	"testing"

	"github.com/l4ci/rota/internal/verdict"
)

func TestTrainBlockedSecondOpinionAndStaleFail(t *testing.T) {
	rec := func(kind, v, sha string) verdict.Record { return verdict.Record{Kind: kind, Verdict: v, Sha: sha} }
	for name, tc := range map[string]struct {
		recs   []verdict.Record
		runner string
		want   bool
	}{
		"second opinion fail": {[]verdict.Record{rec(verdict.SecondOpinion, verdict.Fail, "bbbbbbb")}, "claude", true},
		"second opinion pass": {[]verdict.Record{rec(verdict.SecondOpinion, verdict.Pass, "bbbbbbb")}, "claude", false},
		"stale review fail still blocks": {[]verdict.Record{
			rec(verdict.ReviewSpec, verdict.Fail, "aaaaaaa"),
			rec(verdict.SecondOpinion, verdict.Pass, "bbbbbbb"),
		}, "claude", true},
		"stale second opinion fail still blocks": {[]verdict.Record{
			rec(verdict.SecondOpinion, verdict.Fail, "aaaaaaa"),
			rec(verdict.ReviewSpec, verdict.Pass, "bbbbbbb"),
		}, "claude", true},
		"newer review pass clears older fail": {[]verdict.Record{
			rec(verdict.ReviewSpec, verdict.Fail, "aaaaaaa"),
			rec(verdict.ReviewSpec, verdict.Pass, "bbbbbbb"),
		}, "claude", false},
	} {
		root := t.TempDir()
		for _, r := range tc.recs {
			if _, err := verdict.AddBranch(root, "w1", r); err != nil {
				t.Fatal(err)
			}
		}
		if got := trainBlocked(verdict.Load(root), "w1", verdict.Settings{}); got != tc.want {
			t.Errorf("%s: blocked %v want %v", name, got, tc.want)
		}
	}
}

// A lens FAIL, or a lens record with a blocking finding, keeps a PR out of a
// nudged train like a review-cross one (#2064).
func TestTrainBlockedLens(t *testing.T) {
	major := []verdict.Finding{{Severity: "major", Title: "x"}}
	for name, tc := range map[string]struct {
		rec  verdict.Record
		want bool
	}{
		"security fail":            {verdict.Record{Kind: verdict.ReviewSecurity, Verdict: verdict.Fail, Sha: "a"}, true},
		"ux concerns with a major": {verdict.Record{Kind: verdict.ReviewUX, Verdict: verdict.Concerns, Sha: "a", Findings: major}, true},
		"perf pass":                {verdict.Record{Kind: verdict.ReviewPerf, Verdict: verdict.Pass, Sha: "a"}, false},
	} {
		root := t.TempDir()
		if _, err := verdict.AddBranch(root, "w1", tc.rec); err != nil {
			t.Fatal(err)
		}
		if got := trainBlocked(verdict.Load(root), "w1", verdict.Settings{}); got != tc.want {
			t.Errorf("%s: blocked %v want %v", name, got, tc.want)
		}
	}
}
