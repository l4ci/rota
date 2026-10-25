package round

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/worker"
)

func (f *moveFx) accept(id string, mod func(*AcceptInput)) (Accepted, error) {
	in := AcceptInput{ID: id}
	if mod != nil {
		mod(&in)
	}
	return f.env.Accept(bg, f.root, f.be, in)
}

func (f *moveFx) doneNoIssues(t *testing.T, state string) {
	t.Helper()
	if err := editSlot(f.root, "ben", func(s *worker.Slot) error { return s.MarkState(state, "") }); err != nil {
		t.Fatal(err)
	}
}

func TestAcceptClosesReviewWithRecordedIssues(t *testing.T) {
	f := newMoveFx(t)
	f.mintReview(t, "12")
	f.reportIssues(t, "ben", "#139", "#140")

	res, err := f.accept("12", nil)
	if err != nil || !res.Changed || res.Slot != "ben" || strings.Join(res.Issues, ",") != "#139,#140" {
		t.Fatalf("%+v %v", res, err)
	}
	c := f.be.comments["12"]
	if !f.be.items["12"].Closed || c[len(c)-2] != "Review done: filed #139, #140." {
		t.Errorf("closed with the issues commented: %v %v", f.be.items["12"].Closed, c)
	}
	if f.be.claims["12"] != "" || f.be.bstates["12"] == "in-progress" || len(f.queued()) != 0 {
		t.Errorf("claim, state, queue: %v %v %v", f.be.claims, f.be.bstates, f.queued())
	}
	if s := f.slot("ben"); s.Task() != "" || len(s.Issues()) != 0 {
		t.Errorf("slot freed: %v", s)
	}
	if _, err := f.assign("13", "ben"); err != nil {
		t.Fatalf("assignable right after, no reclaim: %v", err)
	}
}

func TestAcceptClosesReviewWithoutIssuesFlag(t *testing.T) {
	t.Run("issues flag supplies them", func(t *testing.T) {
		f := newMoveFx(t)
		f.mintReview(t, "12")
		f.doneNoIssues(t, "done")
		res, err := f.accept("12", func(in *AcceptInput) { in.Issues = []string{"#7", "#8"} })
		if err != nil || !res.Changed || !f.be.items["12"].Closed {
			t.Fatalf("%+v %v", res, err)
		}
	})
	t.Run("neither names --issues", func(t *testing.T) {
		f := newMoveFx(t)
		f.mintReview(t, "12")
		f.doneNoIssues(t, "done")
		_, err := f.accept("12", nil)
		if blockedBy(t, err) != BlockNoIssues || !strings.Contains(err.Error(), "--issues") {
			t.Fatalf("%v", err)
		}
		if f.be.items["12"].Closed || f.slot("ben").Task() != "12" {
			t.Error("a refusal moves nothing")
		}
	})
	t.Run("gist replaces the note", func(t *testing.T) {
		f := newMoveFx(t)
		f.mintReview(t, "12")
		f.reportIssues(t, "ben", "#139")
		if _, err := f.accept("12", func(in *AcceptInput) { in.Gist = "two refactors" }); err != nil {
			t.Fatal(err)
		}
		c := f.be.comments["12"]
		if c[len(c)-2] != "Review done: two refactors." {
			t.Errorf("%v", c)
		}
	})
}

func TestAcceptRefusals(t *testing.T) {
	t.Run("ordinary item, even with --issues", func(t *testing.T) {
		f := newMoveFx(t)
		f.reportIssues(t, "ben", "#139")
		_, err := f.accept("12", func(in *AcceptInput) { in.Issues = []string{"#1"} })
		if blockedBy(t, err) != BlockSlotBusy || !strings.Contains(err.Error(), "needs a PR") {
			t.Fatalf("%v", err)
		}
		if f.be.items["12"].Closed || f.slot("ben").Task() != "12" {
			t.Error("a refusal moves nothing")
		}
	})
	t.Run("title alone is not minted", func(t *testing.T) {
		f := newMoveFx(t)
		f.be.items["12"].Title = ReviewTitle("cli")
		f.reportIssues(t, "ben", "#139")
		if _, err := f.accept("12", nil); blockedBy(t, err) != BlockReviewNotMinted || f.be.items["12"].Closed {
			t.Fatalf("%v", err)
		}
	})
	t.Run("dirty worktree", func(t *testing.T) {
		f := newMoveFx(t)
		f.mintReview(t, "12")
		f.reportIssues(t, "ben", "#139")
		os.WriteFile(filepath.Join(f.wt("ben"), "dirty.txt"), []byte("wip"), 0o644)
		_, err := f.accept("12", nil)
		if blockedBy(t, err) != BlockSlotBusy || !strings.Contains(err.Error(), "uncommitted") || f.be.items["12"].Closed {
			t.Fatalf("%v", err)
		}
	})
	t.Run("busy slot", func(t *testing.T) {
		f := newMoveFx(t)
		f.mintReview(t, "12")
		if _, err := f.accept("12", func(in *AcceptInput) { in.Issues = []string{"#1"} }); blockedBy(t, err) != BlockSlotBusy || f.be.items["12"].Closed {
			t.Fatalf("%v", err)
		}
	})
	for _, kind := range []string{worker.OutcomePartial, worker.OutcomeFailed} {
		t.Run(kind, func(t *testing.T) {
			f := newMoveFx(t)
			f.mintReview(t, "12")
			f.reportIssues(t, "ben", "#139")
			if err := editSlot(f.root, "ben", func(s *worker.Slot) error {
				s.SetOutcome(kind, []string{"a", "b", "c"}, "2026-10-20T00:00:00Z")
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := f.accept("12", nil); blockedBy(t, err) != BlockSlotBusy || f.be.items["12"].Closed {
				t.Fatalf("%v", err)
			}
		})
	}
	t.Run("no slot holds the id", func(t *testing.T) {
		f := newMoveFx(t)
		if _, err := f.accept("13", nil); blockedBy(t, err) != BlockNoSlotHolds {
			t.Fatalf("%v", err)
		}
	})
}

func TestAcceptAcceptsABlockedSlot(t *testing.T) {
	f := newMoveFx(t)
	f.mintReview(t, "12")
	f.reportIssues(t, "ben", "#139")
	f.doneNoIssues(t, "blocked")
	if res, err := f.accept("12", nil); err != nil || !res.Changed {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestAcceptTwiceChangesNothing(t *testing.T) {
	f := newMoveFx(t)
	f.mintReview(t, "12")
	f.reportIssues(t, "ben", "#139")
	if _, err := f.accept("12", nil); err != nil {
		t.Fatal(err)
	}
	n := len(f.be.comments["12"])
	res, err := f.accept("12", nil)
	if err != nil || res.Changed || len(f.be.comments["12"]) != n {
		t.Fatalf("rerun: %+v %v comments %d -> %d", res, err, n, len(f.be.comments["12"]))
	}
}

// The comment assign posts must open with the marker findingSinceAssign looks
// for, and a finding left before it must not count.
func TestFindingSinceAssignMatchesAssignNote(t *testing.T) {
	note := assignNote("ben", "ben/12-x")
	if !strings.HasPrefix(note, assignMarker) {
		t.Fatalf("assign note %q does not open with marker %q", note, assignMarker)
	}
	f := newMoveFx(t)
	f.be.comments["12"] = []string{"early finding", note}
	f.be.kinds["12"] = []string{"finding", "feedback"}
	if findingSinceAssign(f.be, "12") {
		t.Error("a finding before the assign note counted")
	}
	f.be.comments["12"] = append(f.be.comments["12"], "late finding")
	f.be.kinds["12"] = append(f.be.kinds["12"], "finding")
	if !findingSinceAssign(f.be, "12") {
		t.Error("a finding after the assign note did not count")
	}
}
