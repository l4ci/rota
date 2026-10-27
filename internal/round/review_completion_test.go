package round

import (
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/worker"
)

// Exercise the actual completion writers, reload from disk, then accept the
// minted review. Empty, nonempty and absent evidence traverse the same seam.
func TestReviewCompletionSeam(t *testing.T) {
	for _, route := range []string{"pane", "solo"} {
		for _, tc := range []struct {
			name, issues, note string
			refs               []string
			complete           bool
		}{
			{name: "no findings", issues: "none", note: "no findings", complete: true},
			{name: "filed issues", issues: "139, PROJ-140", refs: []string{"#139", "PROJ-140"}, note: "filed #139, PROJ-140", complete: true},
			{name: "missing evidence"},
		} {
			t.Run(route+"/"+tc.name, func(t *testing.T) {
				f := newMoveFx(t)
				f.mintReview(t, "12")
				if route == "pane" {
					evidence := ""
					if tc.issues != "" {
						evidence = "issues:" + tc.issues
					}
					f.host.capture = "ROTA-DONE ben " + evidence + "\n"
					if _, err := f.ws.Poll(bg, f.root, worker.PollOpts{Slot: "ben"}); err != nil {
						t.Fatal(err)
					}
				} else {
					setHost(t, f.root, host.Solo)
					opts := ReportOpts{Slot: "ben", State: "done", Issues: tc.issues, Attempt: f.slot("ben").Attempt()}
					if _, err := f.env.ReportSlot(bg, f.root, opts); err != nil {
						t.Fatal(err)
					}
					if res, err := f.env.ReportSlot(bg, f.root, opts); err != nil || res.Changed {
						t.Fatalf("repeat report: %+v %v", res, err)
					}
				}
				s := f.slot("ben") // fresh registry read proves the distinction persisted
				if s.State() != "done" || s.PR() != "" || s.ReviewResult().Complete() != tc.complete || !slices.Equal(s.Issues(), tc.refs) {
					t.Fatalf("completion: %v", s.Raw())
				}
				mod := func(in *AcceptInput) {
					if !tc.complete {
						in.Gist = "a gist cannot replace evidence"
					}
				}
				res, err := f.accept("12", mod)
				if !tc.complete {
					if blockedBy(t, err) != BlockNoIssues || !strings.Contains(err.Error(), "--issues") {
						t.Fatalf("missing evidence: %+v %v", res, err)
					}
					if f.be.items["12"].Closed || f.slot("ben").Task() != "12" {
						t.Fatal("refusal moved the review")
					}
					return
				}
				if err != nil || !res.Changed || res.Slot != "ben" || !slices.Equal(res.Issues, tc.refs) || res.NoFindings != (tc.issues == "none") {
					t.Fatalf("accept: %+v %v", res, err)
				}
				if !f.be.items["12"].Closed || !slices.Contains(f.be.comments["12"], "Review done: "+tc.note+".") {
					t.Fatalf("close: %v", f.be.comments["12"])
				}
				if s := f.slot("ben"); s.Task() != "" || s.PR() != "" || s.ReviewResult().Complete() {
					t.Fatalf("slot not freed: %v", s.Raw())
				}
				if f.be.claims["12"] != "" || f.be.bstates["12"] == "in-progress" || len(f.queued()) != 0 {
					t.Fatal("claim, state or PR queue not cleared")
				}
				if _, err := f.assign("13", "ben"); err != nil {
					t.Fatalf("freed slot cannot be assigned: %v", err)
				}
				if f.slot("ben").ReviewResult().Complete() {
					t.Fatal("review evidence leaked into next assignment")
				}
			})
		}
	}
}

// Assign and transfer keep their existing fallback acceptance path, including
// an accurate empty-review note and clearing evidence before the next binding.
func TestReviewNoFindingsFallback(t *testing.T) {
	for _, action := range []string{"assign", "transfer"} {
		t.Run(action, func(t *testing.T) {
			f := newMoveFx(t)
			f.mintReview(t, "12")
			f.host.capture = "ROTA-DONE ben issues:none\n"
			if _, err := f.ws.Poll(bg, f.root, worker.PollOpts{Slot: "ben"}); err != nil {
				t.Fatal(err)
			}
			if action == "assign" {
				if _, err := f.assign("13", "ben"); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := f.assign("13", "dana"); err != nil {
					t.Fatal(err)
				}
				if _, err := f.transfer("13", "ben", nil); err != nil {
					t.Fatal(err)
				}
			}
			if !f.be.items["12"].Closed || !slices.Contains(f.be.comments["12"], "Review done: no findings.") {
				t.Fatalf("close: %v", f.be.comments["12"])
			}
			if s := f.slot("ben"); s.Task() != "13" || s.ReviewResult().Complete() || s.PR() != "" {
				t.Fatalf("binding: %v", s.Raw())
			}
			if f.be.claims["12"] != "" || len(f.queued()) != 0 {
				t.Fatal("review claim or PR queue remains")
			}
		})
	}
}
