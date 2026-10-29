package round

import (
	"errors"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/backlog/trackertest"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/projects"
	"github.com/l4ci/rota/internal/proof"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/tracker"
)

func reviewAcceptanceBoard(t *testing.T, f *moveFx) (*backlog.Issues, *trackertest.Fake) {
	t.Helper()
	f.mintReview(t, "12")
	f.reportIssues(t, "ben", "#139", "#140")
	remote := &trackertest.Fake{Issues: []tracker.Issue{{Key: tracker.NumKey(12), Title: ReviewTitle("cli"), State: "open", Labels: []string{"type:task", "in-progress"}, Body: "## Acceptance\n- [ ] AC-1: review ran\n- [ ] AC-2: findings filed\n"}}}
	be := &backlog.Issues{Cfg: config.Load(projects.OSEnv(), rotatree.Config(f.root)), Tracker: remote, CountProof: proof.CountRows, LookupProof: proof.Lookup}
	if won, _, err := be.Claim("12", f.slot("ben").ClaimID()); err != nil || !won {
		t.Fatalf("claim: %v %v", won, err)
	}
	return be, remote
}

func TestAcceptReviewRecordsUnmetAcceptanceReason(t *testing.T) {
	f := newMoveFx(t)
	be, remote := reviewAcceptanceBoard(t, f)
	res, err := f.env.Accept(bg, f.root, be, AcceptInput{ID: "12"})
	if err != nil || !res.Changed {
		t.Fatalf("review findings must remain acceptable: %+v %v", res, err)
	}
	if remote.Issues[0].State != "closed" || f.slot("ben").Task() != "" {
		t.Fatal("review or slot was left half closed")
	}
	var comments string
	for _, c := range remote.Issues[0].Comments {
		comments += c.Body + "\n"
	}
	for _, want := range []string{"Unmet acceptance criteria:", "AC-1: review ran", "AC-2: findings filed", "Reason:", "filed #139, #140"} {
		if !strings.Contains(comments, want) {
			t.Errorf("closing record lacks %q: %s", want, comments)
		}
	}
}

func TestAutomaticReviewCloseRecordsUnmetAcceptanceReason(t *testing.T) {
	f := newMoveFx(t)
	be, remote := reviewAcceptanceBoard(t, f)
	if err := f.env.closeReview(bg, f.root, be, f.slot("ben")); err != nil {
		t.Fatal(err)
	}
	if remote.Issues[0].State != "closed" || f.slot("ben").Task() != "" {
		t.Fatal("automatic review close left the item or slot unfinished")
	}
	var text string
	for _, c := range remote.Issues[0].Comments {
		text += c.Body
	}
	if !strings.Contains(text, "Reason: Review done: filed #139, #140") {
		t.Fatalf("missing artifact-based reason: %s", text)
	}
}

func researchAcceptanceBoard(t *testing.T, f *moveFx) (*backlog.Issues, *trackertest.Fake) {
	t.Helper()
	be, remote := reviewAcceptanceBoard(t, f)
	remote.Issues[0].Labels = append(remote.Issues[0].Labels, "decision:research")
	remote.Issues[0].Milestone = "M01 — Decisions"
	remote.Issues = append(remote.Issues, tracker.Issue{Key: tracker.NumKey(20), Title: "M01 — Decisions", State: "open", Labels: []string{"milestone-tracker", "status:active"}, Milestone: "M01 — Decisions", Body: "---\nid: M01\nstatus: active\n---\n\n## Decisions\n\n"})
	be.Tracker = &trackertest.MS{Fake: remote, Native: []tracker.Milestone{{Number: 1, Title: "M01 — Decisions", State: "open"}}}
	if _, err := be.AddComment("12", "feedback", assignNote("ben", "ben/12-review")); err != nil {
		t.Fatal(err)
	}
	if _, err := be.AddComment("12", "finding", "Use X."); err != nil {
		t.Fatal(err)
	}
	return be, remote
}

func TestAcceptResearchRecordsUnmetAcceptanceReason(t *testing.T) {
	f := newMoveFx(t)
	be, remote := researchAcceptanceBoard(t, f)
	if res, err := f.env.Accept(bg, f.root, be, AcceptInput{ID: "12", Gist: "Use X"}); err != nil || !res.Changed {
		t.Fatalf("research accept: %+v %v", res, err)
	}
	var text string
	for _, c := range remote.Issues[0].Comments {
		text += c.Body
	}
	if !strings.Contains(text, "Reason: Finding accepted: Use X") || remote.Issues[0].State != "closed" || f.slot("ben").Task() != "" {
		t.Fatalf("research completion: %s", text)
	}
	if !strings.Contains(remote.Issues[1].Body, "- #12: Use X") {
		t.Fatalf("decision missing: %s", remote.Issues[1].Body)
	}
}

func TestNoPRAcceptancePreflightFailureMovesNothing(t *testing.T) {
	for _, research := range []bool{false, true} {
		name := "review"
		if research {
			name = "research"
		}
		t.Run(name, func(t *testing.T) {
			f := newMoveFx(t)
			be, remote := reviewAcceptanceBoard(t, f)
			if research {
				be, remote = researchAcceptanceBoard(t, f)
			}
			wantError := "notes unavailable"
			if research {
				remote.Issues[0].Comments = append(remote.Issues[0].Comments, tracker.Comment{ID: "999", Body: "<!-- rota:acceptance 999999999999999999999999/1 -->\ninvalid note\n"})
				wantError = "number out of range"
			}
			branch := gitIn(t, f.wt("ben"), "branch", "--show-current")
			comments := len(remote.Issues[0].Comments)
			decisionBody := ""
			if research {
				decisionBody = remote.Issues[1].Body
			}
			if !research {
				remote.Fail = map[string]error{"comments": errors.New("notes unavailable")}
			}
			_, err := f.env.Accept(bg, f.root, be, AcceptInput{ID: "12", Gist: "Use X"})
			if err == nil || !strings.Contains(err.Error(), wantError) {
				t.Fatalf("missing read failure: %v", err)
			}
			if got := gitIn(t, f.wt("ben"), "branch", "--show-current"); got != branch {
				t.Fatalf("preflight failure parked branch %s as %s", branch, got)
			}
			if remote.Issues[0].State != "open" || len(remote.Issues[0].Comments) != comments || f.slot("ben").Task() != "12" {
				t.Fatal("preflight failure changed the item, comments or held slot")
			}
			if research && remote.Issues[1].Body != decisionBody {
				t.Fatal("preflight failure wrote the milestone decision")
			}
		})
	}
}

func TestAcceptReviewWithMetAcceptance(t *testing.T) {
	f := newMoveFx(t)
	be, remote := reviewAcceptanceBoard(t, f)
	for kind, text := range map[string]string{
		"proof":      "## Proof\n- 2026-10-27 · filed issues · PASS · abc1234 · #139, #140\n",
		"acceptance": "- AC-1 · 2026-10-27 · abc1234 · filed issues · review ran\n- AC-2 · 2026-10-27 · abc1234 · filed issues · findings filed\n",
	} {
		if _, err := be.NotePut("12", kind, text); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.env.Accept(bg, f.root, be, AcceptInput{ID: "12"}); err != nil {
		t.Fatal(err)
	}
	if remote.Issues[0].State != "closed" || f.slot("ben").Task() != "" {
		t.Fatal("met review did not close and free the slot")
	}
	for _, c := range remote.Issues[0].Comments {
		if strings.Contains(c.Body, "Unmet acceptance criteria:") {
			t.Fatalf("met criteria were reported unmet: %s", c.Body)
		}
	}
}
