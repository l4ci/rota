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

// The tick asks verdict.BlockingGate, which the verdict seam tests cover;
// this pins only the wiring: review:none lets a stale cross FAIL through (#3479).
func TestReadinessStaleCrossFail(t *testing.T) {
	const head = "bbbbbbb1234"
	stale := verdict.Record{Kind: verdict.ReviewCross, Verdict: verdict.Fail, Sha: "aaaaaaa", RecordedAt: "2026-10-30T09:00:00Z"}
	for _, none := range []bool{true, false} {
		in := ReadinessInput{Head: head, PR: "#7", ReviewOff: true, Records: []verdict.Record{stale}, CrossNone: func() bool { return none }}
		if got := ReadinessOf(in).Ready; got != none {
			t.Errorf("review:none %v: ready = %v, want %v", none, got, none)
		}
	}
}
