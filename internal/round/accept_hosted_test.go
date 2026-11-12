package round

import (
	"github.com/l4ci/rota/internal/blockedby"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/worker"
)

func TestAcceptHostedReviewWithoutPriorPoll(t *testing.T) {
	for _, kind := range []host.Kind{host.Tmux, host.Herdr, host.HerdrCore} {
		for _, evidence := range []string{"issues:none", "issues:#139,#140"} {
			t.Run(kind.String()+"/"+evidence, func(t *testing.T) {
				f := newMoveFx(t)
				f.mintReview(t, "12")
				setHost(t, f.root, kind)
				f.host.name = kind
				f.host.capture = "ROTA-DONE ben " + evidence + "\n"
				if got := f.slot("ben").State(); got != "busy" {
					t.Fatalf("expected unpolled busy slot, got %q", got)
				}
				res, err := f.accept("12", nil)
				if err != nil || !res.Changed || res.NoFindings != (evidence == "issues:none") {
					t.Fatalf("accept must read current completion without a manual poll: %+v %v", res, err)
				}
				if evidence != "issues:none" && !slices.Equal(res.Issues, []string{"#139", "#140"}) {
					t.Fatalf("lost pane evidence: %+v", res)
				}
				if !f.be.items["12"].Closed || f.slot("ben").Task() != "" {
					t.Fatal("accept did not close review and free slot")
				}
			})
		}
	}
}

func TestAcceptHostedReviewKeepsCompletionGuards(t *testing.T) {
	for _, name := range []string{"blocked without evidence", "missing evidence", "dirty", "partial", "pending relay", "solo"} {
		t.Run(name, func(t *testing.T) {
			f := newMoveFx(t)
			f.mintReview(t, "12")
			f.host.capture = "ROTA-DONE ben issues:none\n"
			want := blockedby.SlotBusy
			switch name {
			case "blocked without evidence":
				f.host.capture = "ROTA-BLOCKED ben: wait\n"
				// A blocked state can settle, but cannot invent review evidence.
				want = blockedby.NoIssues
			case "missing evidence":
				f.host.capture = "ROTA-DONE ben issues:\n"
				want = blockedby.NoIssues
			case "dirty":
				if err := os.WriteFile(filepath.Join(f.wt("ben"), "dirty.txt"), []byte("wip"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "partial":
				if err := editSlot(f.root, "ben", func(s *worker.Slot) error {
					s.SetOutcome(worker.OutcomePartial, []string{"changed", "found", "remains"}, f.now.Format("2006-01-02T15:04:05Z"))
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			case "pending relay":
				if err := editSlot(f.root, "ben", func(s *worker.Slot) error {
					s.SetRelayPending(1, f.now.Format("2006-01-02T15:04:05Z"))
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			case "solo":
				setHost(t, f.root, host.Solo)
				noHost(t, f.wire, &f.env)
			}
			res, err := f.accept("12", nil)
			if blockedBy(t, err) != want || res.Changed || f.be.items["12"].Closed || f.slot("ben").Task() != "12" {
				t.Fatalf("guard failed: %+v %v", res, err)
			}
		})
	}
}

func TestHostedReviewBriefEndsWithReviewCompletion(t *testing.T) {
	f := newAssignFixture(t)
	f.be.items["12"].Title = ReviewTitle("cli")
	if _, err := f.assign("12", "ben", nil); err != nil {
		t.Fatal(err)
	}
	text := f.host.sent
	for _, want := range []string{"ROTA-DONE ben issues:#a,#b", "ROTA-DONE ben issues:none", "open no PR", "Hosted workers report completion by printing"} {
		if !strings.Contains(text, want) {
			t.Errorf("review brief missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "ROTA-DONE ben <pr-url>") || strings.Contains(text, "Pre-flight before `rota worker done`") {
		t.Errorf("review brief asks for a PR or code proof:\n%s", text)
	}
}
