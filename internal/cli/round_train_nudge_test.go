package cli

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/roundtick"
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

func TestTickNeedsMergeOnlyForMergeItems(t *testing.T) {
	cases := []struct {
		name string
		r    roundtick.Result
		want bool
	}{
		{"empty tick", roundtick.Result{}, false},
		{"autopilot merged", roundtick.Result{Did: []roundtick.Action{{Action: "merge", Target: "#9"}}}, false},
		{"gate failed only", roundtick.Result{NeedsYou: []roundtick.Item{{Kind: "gate-failed", Target: "#9"}}}, false},
		{"merge item", roundtick.Result{NeedsYou: []roundtick.Item{{Kind: "drift"}, {Kind: "merge", Target: "#9"}}}, true},
	}
	for _, c := range cases {
		if got := tickNeedsMerge(c.r); got != c.want {
			t.Errorf("%s: tickNeedsMerge = %v, want %v", c.name, got, c.want)
		}
	}
}
