package roundtick

import (
	"testing"
	"time"

	"github.com/l4ci/rota/internal/verdict"
)

func TestReadinessOf(t *testing.T) {
	const head = "bbbbbbb1234"
	at := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	done := at.Add(-time.Hour)
	rec := func(kind, v, sha string) verdict.Record {
		return verdict.Record{Kind: kind, Verdict: v, Sha: sha, RecordedAt: at.Format(time.RFC3339)}
	}
	blocking := verdict.Finding{Severity: "major", Title: "x"}
	for name, tc := range map[string]struct {
		in   ReadinessInput
		want Readiness
	}{
		"head-matched review": {
			ReadinessInput{Head: head, PR: "#7", Records: []verdict.Record{rec(verdict.ReviewSpec, verdict.Pass, head)}},
			Readiness{Ready: true, Since: at}},
		"review of a previous head": {
			ReadinessInput{Head: head, PR: "#7", Records: []verdict.Record{rec(verdict.ReviewSpec, verdict.Pass, "aaaaaaa")}},
			Readiness{}},
		"no review": {ReadinessInput{Head: head, PR: "#7"}, Readiness{}},
		"unresolved head": {
			ReadinessInput{PR: "#7", Records: []verdict.Record{rec(verdict.ReviewSpec, verdict.Pass, head)}},
			Readiness{}},
		"blocking finding": {
			ReadinessInput{Head: head, PR: "#7", Records: []verdict.Record{
				{Kind: verdict.ReviewQuality, Verdict: verdict.Concerns, Sha: head, Findings: []verdict.Finding{blocking}}}},
			Readiness{}},
		"held on its head": {
			ReadinessInput{Head: head, PR: "#7", Names: []string{"w1"},
				State:   State{Held: map[string]string{"w1": "verify-failed"}},
				Records: []verdict.Record{rec(verdict.ReviewSpec, verdict.Pass, head)}},
			Readiness{}},
		"sent back, not pushed to": {
			ReadinessInput{Head: head, PR: "#7", BounceHead: "bbbbbbb",
				Records: []verdict.Record{rec(verdict.ReviewSpec, verdict.Pass, head)}},
			Readiness{}},
		"review off dates from done": {
			ReadinessInput{Head: head, PR: "#7", ReviewOff: true, DoneAt: done}, Readiness{Ready: true, Since: done}},
		"review off, no date is immediate": {
			ReadinessInput{Head: head, PR: "#7", ReviewOff: true}, Readiness{Ready: true, Immediate: true}},
		"review off still blocked": {
			ReadinessInput{Head: head, PR: "#7", ReviewOff: true, Records: []verdict.Record{rec(verdict.ReviewSpec, verdict.Fail, head)}},
			Readiness{}},
	} {
		t.Run(name, func(t *testing.T) {
			if got := ReadinessOf(tc.in); got != tc.want {
				t.Errorf("got %+v want %+v", got, tc.want)
			}
		})
	}
}

func TestTrainBlocked(t *testing.T) {
	rec := func(kind, v, sha string) verdict.Record { return verdict.Record{Kind: kind, Verdict: v, Sha: sha} }
	major := []verdict.Finding{{Severity: "major", Title: "x"}}
	minor := []verdict.Finding{{Severity: "minor", Title: "x"}}
	with := func(kind, v string, f []verdict.Finding) verdict.Record {
		return verdict.Record{Kind: kind, Verdict: v, Sha: "a", Findings: f}
	}
	for name, tc := range map[string]struct {
		recs []verdict.Record
		want bool
	}{
		"none":                           {nil, false},
		"review pass":                    {[]verdict.Record{rec(verdict.ReviewSpec, verdict.Pass, "a")}, false},
		"review concerns":                {[]verdict.Record{rec(verdict.ReviewQuality, verdict.Concerns, "a")}, false},
		"spec fail after pass":           {[]verdict.Record{rec(verdict.ReviewQuality, verdict.Pass, "a"), rec(verdict.ReviewSpec, verdict.Fail, "a")}, true},
		"second opinion fail":            {[]verdict.Record{rec(verdict.SecondOpinion, verdict.Fail, "b")}, true},
		"second opinion pass":            {[]verdict.Record{rec(verdict.SecondOpinion, verdict.Pass, "b")}, false},
		"stale review fail still blocks": {[]verdict.Record{rec(verdict.ReviewSpec, verdict.Fail, "a"), rec(verdict.SecondOpinion, verdict.Pass, "b")}, true},
		"newer review pass clears fail":  {[]verdict.Record{rec(verdict.ReviewSpec, verdict.Fail, "a"), rec(verdict.ReviewSpec, verdict.Pass, "b")}, false},
		"concerns with blocking finding": {[]verdict.Record{with(verdict.ReviewQuality, verdict.Concerns, major)}, true},
		"concerns with minor finding":    {[]verdict.Record{with(verdict.ReviewQuality, verdict.Concerns, minor)}, false},
		"cross concerns with blocker":    {[]verdict.Record{with(verdict.ReviewCross, verdict.Concerns, major)}, true},
		"cross blocker then newer clean": {[]verdict.Record{with(verdict.ReviewCross, verdict.Concerns, major), with(verdict.ReviewCross, verdict.Pass, nil)}, false},
		"security fail":                  {[]verdict.Record{rec(verdict.ReviewSecurity, verdict.Fail, "a")}, true},
		"ux concerns with a major":       {[]verdict.Record{with(verdict.ReviewUX, verdict.Concerns, major)}, true},
		"perf pass":                      {[]verdict.Record{rec(verdict.ReviewPerf, verdict.Pass, "a")}, false},
	} {
		if got := TrainBlocked(tc.recs); got != tc.want {
			t.Errorf("%s: blocked %v want %v", name, got, tc.want)
		}
	}
}
