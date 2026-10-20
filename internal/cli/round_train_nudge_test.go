package cli

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/verdict"
)

func TestTrainNudgeNamesTwoReviewedPRsOnOneBase(t *testing.T) {
	ns := trainNudges([]waitingPR{
		{PR: "https://example.test/o/r/pull/12", Base: "main", Reviewed: true},
		{PR: "#9", Base: "main", Reviewed: true},
		{PR: "#30", Base: "main", Reviewed: false},
		{PR: "#31", Base: "cycle", Reviewed: true},
	})
	if len(ns) != 1 {
		t.Fatalf("nudges = %+v, want one (main)", ns)
	}
	if got, want := ns[0].Command(), "rota worker train 9 12 --base main"; got != want {
		t.Errorf("command = %q, want %q", got, want)
	}
	if txt := ns[0].Text(); !strings.Contains(txt, "#9 #12") || !strings.Contains(txt, "rota worker train") {
		t.Errorf("text names neither PRs nor command: %q", txt)
	}
}

func TestTrainNudgeAbsentWithOneReviewedPR(t *testing.T) {
	for name, w := range map[string][]waitingPR{
		"one waiting":            {{PR: "#9", Base: "main", Reviewed: true}},
		"second unreviewed":      {{PR: "#9", Base: "main", Reviewed: true}, {PR: "#10", Base: "main"}},
		"two on different bases": {{PR: "#9", Base: "main", Reviewed: true}, {PR: "#10", Base: "cycle", Reviewed: true}},
		"none":                   nil,
	} {
		if ns := trainNudges(w); len(ns) != 0 {
			t.Errorf("%s: nudges = %+v, want none", name, ns)
		}
	}
}

func TestTrainNudgeGroupsEmptyBaseWithDefault(t *testing.T) {
	w := fillDefaultBase([]waitingPR{
		{PR: "#9", Base: "", Reviewed: true},
		{PR: "#10", Base: "main", Reviewed: true},
	}, "main")
	ns := trainNudges(w)
	if len(ns) != 1 || ns[0].Base != "main" || len(ns[0].PRs) != 2 {
		t.Fatalf("nudges = %+v, want one train of two on main", ns)
	}
}

func TestTrainBlockedOnFailNotConcerns(t *testing.T) {
	rec := func(kind, v string) verdict.Record { return verdict.Record{Kind: kind, Verdict: v, Sha: "bbbbbbb"} }
	for name, tc := range map[string]struct {
		recs []verdict.Record
		want bool
	}{
		"pass":                 {[]verdict.Record{rec(verdict.ReviewSpec, verdict.Pass)}, false},
		"concerns":             {[]verdict.Record{rec(verdict.ReviewQuality, verdict.Concerns)}, false},
		"fail":                 {[]verdict.Record{rec(verdict.ReviewSpec, verdict.Fail)}, true},
		"spec fail after pass": {[]verdict.Record{rec(verdict.ReviewQuality, verdict.Pass), rec(verdict.ReviewSpec, verdict.Fail)}, true},
		"none":                 {nil, false},
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
