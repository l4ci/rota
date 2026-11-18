package worker

import (
	"context"
	"fmt"
	gateaudit "github.com/l4ci/rota/internal/gate"
	"github.com/l4ci/rota/internal/landing"
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
		{"partial", false, landing.GateOutcomeOpen},
		{"failed", false, landing.GateOutcomeOpen},
		{"partial", true, landing.GateOutcomeOpen},
		{"done", false, landing.GatePass},
		{"", false, landing.GatePass},
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
			if c.want != landing.GateOutcomeOpen {
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
		{"done", landing.GatePass},
		{"partial", landing.GateOutcomeOpen},
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

// decisionIssues serves issue #5 with one criterion and the comments it is met by.
type decisionIssues struct {
	issueStub
	comments []tracker.Comment
}

func (s *decisionIssues) Get(_ context.Context, n tracker.IssueKey, _ bool) (tracker.Issue, error) {
	return tracker.Issue{Key: n, State: "open", Body: "## Acceptance\n- [ ] AC-1: ready\n", Comments: s.comments}, nil
}

// writerIssues also reports who has write access, as GitHub does.
type writerIssues struct {
	decisionIssues
	writers map[string]bool
}

func (s *writerIssues) CanPush(_ context.Context, login string) (bool, error) {
	return s.writers[login], nil
}

// A partial outcome with every criterion met at the gated head is lifted by the
// gate itself and audited; one still open keeps refusing, and a failed outcome
// is never lifted. A decision counts only from a writer, a proof mark only at
// the head, and a lift needs its audit record.
func TestGateLiftsAFullyMetPartial(t *testing.T) {
	decided := func(author string) []tracker.Comment {
		return []tracker.Comment{
			{ID: "7", Author: author, Body: "<!-- rota:comment decision -->\nship it"},
			{ID: "8", Body: "<!-- rota:acceptance -->\n- AC-1 · 2026-11-14 · decision · 7 · ready\n"},
		}
	}
	// proven marks AC-1 at markSha; rows are the proof rows as "RESULT sha".
	proven := func(markSha string, rows ...string) []tracker.Comment {
		var proof strings.Builder
		proof.WriteString("<!-- rota:proof -->\n## Proof\n")
		for _, r := range rows {
			res, sha, _ := strings.Cut(r, " ")
			proof.WriteString("- 2026-11-14 · check · " + res + " · " + sha + " · ok\n")
		}
		return []tracker.Comment{
			{ID: "8", Body: "<!-- rota:acceptance -->\n- AC-1 · 2026-11-14 · " + markSha + " · check · ready\n"},
			{ID: "9", Body: proof.String()},
		}
	}
	for _, c := range []struct {
		name      string
		outcome   string
		comments  func(head string) []tracker.Comment
		noTrust   bool
		badAudit  bool
		want      string
		wantAudit bool
	}{
		{"decision by a writer", "partial", func(string) []tracker.Comment { return decided("maintainer") }, false, false, landing.GatePass, true},
		{"decision by a non-writer", "partial", func(string) []tracker.Comment { return decided("stranger") }, false, false, landing.GateOutcomeOpen, false},
		{"tracker cannot report access", "partial", func(string) []tracker.Comment { return decided("maintainer") }, true, false, landing.GateOutcomeOpen, false},
		{"proof at the head", "partial", func(h string) []tracker.Comment { return proven(h[:7], "PASS "+h[:7]) }, false, false, landing.GatePass, true},
		{"proof at an older sha", "partial", func(string) []tracker.Comment { return proven("deadbee", "PASS deadbee") }, false, false, landing.GateOutcomeOpen, false},
		{"failing proof at the head", "partial", func(h string) []tracker.Comment { return proven(h[:7], "FAIL "+h[:7]) }, false, false, landing.GateOutcomeOpen, false},
		{"stale PASS then FAIL at the head", "partial", func(h string) []tracker.Comment {
			return proven(h[:7], "PASS deadbee", "FAIL "+h[:7])
		}, false, false, landing.GateOutcomeOpen, false},
		{"PASS then FAIL then PASS at the head", "partial", func(h string) []tracker.Comment {
			return proven(h[:7], "PASS "+h[:7], "FAIL "+h[:7], "PASS "+h[:7])
		}, false, false, landing.GatePass, true},
		{"open criterion", "partial", func(string) []tracker.Comment { return nil }, false, false, landing.GateOutcomeOpen, false},
		{"mark on a non-decision comment", "partial", func(string) []tracker.Comment {
			cs := decided("maintainer")
			cs[0].Body = "<!-- rota:comment feedback -->\nship it"
			return cs
		}, false, false, landing.GateOutcomeOpen, false},
		{"met but failed", "failed", func(string) []tracker.Comment { return decided("maintainer") }, false, false, landing.GateOutcomeOpen, false},
		{"audit cannot be written", "partial", func(string) []tracker.Comment { return decided("maintainer") }, false, true, landing.GateOutcomeOpen, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, ghURL)
			head := w.pushStamped("a.txt", "")
			w.setConfig(`{"issues":{"source":"gitlab"},"test":{"full":["true"]}}`)
			w.setOutcomeSlot(c.outcome)
			auditPath := filepath.Join(w.dir, ".rota", "gate-audit.jsonl")
			if c.badAudit {
				if err := os.MkdirAll(auditPath, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			base := decisionIssues{comments: c.comments(strings.TrimSpace(head))}
			var iss Issues = &base
			if !c.noTrust {
				iss = &writerIssues{decisionIssues: base, writers: map[string]bool{"maintainer": true}}
			}
			e := w.env(false)
			e.Issues = func(string, tracker.Settings) (Issues, error) { return iss, nil }
			res, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main"})
			if err != nil || res.Verdict != c.want {
				t.Fatalf("verdict = %q (%v), want %q: %+v", res.Verdict, err, c.want, res)
			}
			recs, _ := gateaudit.ReadAudit(w.dir)
			var lifted []gateaudit.Record
			for _, r := range recs {
				if r.Gate == "outcome" && r.Verb == "lift-partial" && r.Target == "w1" {
					lifted = append(lifted, r)
				}
			}
			if (len(lifted) == 1) != c.wantAudit || len(lifted) > 1 {
				t.Errorf("lift records = %+v, want audited=%v", lifted, c.wantAudit)
			}
			if c.wantAudit && !strings.HasPrefix(lifted[0].Note, "PARTIAL-LIFTED w1") {
				t.Errorf("audit note %q", lifted[0].Note)
			}
		})
	}
}
