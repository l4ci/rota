package worker

import (
	"context"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/tracker"
)

type fakeReviewForge struct {
	notes   []tracker.Comment
	reviews []tracker.Review
}

func (f fakeReviewForge) MRNotes(context.Context, int) ([]tracker.Comment, error) {
	return f.notes, nil
}
func (f fakeReviewForge) Reviews(context.Context, int) ([]tracker.Review, error) {
	return f.reviews, nil
}

func at(min int) time.Time { return time.Date(2026, 10, 8, 10, min, 0, 0, time.UTC) }

func reviewSlot() *Slot {
	s := NewSlot("nia", "nia/577-x", "/wt", "main", "")
	s.SetPR("https://github.com/o/r/pull/9")
	return s
}

func TestPendingReviewFiltersOwnAndApprovals(t *testing.T) {
	f := fakeReviewForge{
		notes: []tracker.Comment{
			{ID: "1", Author: "rev", Body: "please rename x", CreatedAt: at(1)},
			{ID: "2", Author: "rev", Body: "Fixed in abc\n" + marker.Line("worker-reply", "nia"), CreatedAt: at(2)},
			{ID: "3", Author: "rev", Body: "question\n" + marker.Line("escalation", "esc-1"), CreatedAt: at(3)},
		},
		reviews: []tracker.Review{
			{Comment: tracker.Comment{ID: "4", Author: "rev", Body: "LGTM", CreatedAt: at(4)}, State: tracker.ReviewApproved},
			{Comment: tracker.Comment{ID: "5", Author: "bot", Body: "", CreatedAt: at(5)}, State: tracker.ReviewCommented},
			{Comment: tracker.Comment{ID: "6", Author: "rev", Body: "split this file", CreatedAt: at(6)}, State: tracker.ReviewChangesRequested},
		},
	}
	b, err := PendingReview(context.Background(), f, reviewSlot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Items) != 2 || b.Items[0].ID != "1" || b.Items[1].ID != "6" {
		t.Fatalf("items %+v", b.Items)
	}
	if b.PR != "https://github.com/o/r/pull/9" || b.Slot != "nia" || b.Cursor == "" {
		t.Fatalf("batch %+v", b)
	}
}

func TestPendingReviewCursorUnchanged(t *testing.T) {
	f := fakeReviewForge{notes: []tracker.Comment{{ID: "1", Author: "rev", Body: "fix", CreatedAt: at(1)}}}
	s := reviewSlot()
	for i := 0; i < 2; i++ { // polling twice never moves the cursor
		b, err := PendingReview(context.Background(), f, s, nil)
		if err != nil || len(b.Items) != 1 {
			t.Fatalf("poll %d: %+v %v", i, b, err)
		}
	}
	if s.ReviewSeen() != "" {
		t.Fatalf("cursor moved to %q", s.ReviewSeen())
	}
	b, _ := PendingReview(context.Background(), f, s, nil)
	s.SetReviewSeen(b.Cursor)
	b, err := PendingReview(context.Background(), f, s, nil)
	if err != nil || len(b.Items) != 0 || !b.Empty() {
		t.Fatalf("after consuming: %+v %v", b, err)
	}
	f.notes = append(f.notes, tracker.Comment{ID: "2", Author: "rev", Body: "and this", CreatedAt: at(9)})
	if b, _ = PendingReview(context.Background(), f, s, nil); len(b.Items) != 1 || b.Items[0].ID != "2" {
		t.Fatalf("new comment: %+v", b)
	}
}

func TestPendingReviewVerdict(t *testing.T) {
	s := reviewSlot()
	v := &ReviewVerdict{At: at(3), Text: "review FAIL: missing test"}
	b, err := PendingReview(context.Background(), fakeReviewForge{}, s, v)
	if err != nil || b.Verdict != v.Text || b.Empty() {
		t.Fatalf("%+v %v", b, err)
	}
	s.SetReviewSeen(b.Cursor)
	if b, _ = PendingReview(context.Background(), fakeReviewForge{}, s, v); !b.Empty() {
		t.Fatalf("verdict reported twice: %+v", b)
	}
}

func TestPendingReviewNoPR(t *testing.T) {
	s := NewSlot("nia", "b", "/wt", "main", "")
	if b, err := PendingReview(context.Background(), fakeReviewForge{}, s, nil); err != nil || !b.Empty() {
		t.Fatalf("%+v %v", b, err)
	}
}

// An item created in the same second as the newest consumed one is new, not lost.
func TestPendingReviewSameSecondItemIsNotLost(t *testing.T) {
	s := reviewSlot()
	f := fakeReviewForge{notes: []tracker.Comment{{ID: "1", Author: "rev", Body: "first", CreatedAt: at(1)}}}
	b, err := PendingReview(context.Background(), f, s, nil)
	if err != nil || len(b.Items) != 1 {
		t.Fatalf("%+v %v", b, err)
	}
	s.SetReviewSeen(b.Cursor)
	f.notes = append(f.notes, tracker.Comment{ID: "2", Author: "rev", Body: "second", CreatedAt: at(1)})
	f.reviews = []tracker.Review{{Comment: tracker.Comment{ID: "1", Author: "rev", Body: "inline", CreatedAt: at(1)}, Inline: true}}
	b, err = PendingReview(context.Background(), f, s, nil)
	if err != nil || len(b.Items) != 2 || b.Items[0].Body != "second" && b.Items[1].Body != "second" {
		t.Fatalf("same-second items: %+v %v", b.Items, err)
	}
	for _, it := range b.Items {
		if it.Body == "first" {
			t.Fatalf("a consumed item came back: %+v", b.Items)
		}
	}
	s.SetReviewSeen(b.Cursor)
	if b, _ = PendingReview(context.Background(), f, s, nil); !b.Empty() {
		t.Fatalf("consumed batch came back: %+v", b.Items)
	}
}

// A CHANGES_REQUESTED review with no text is still a request.
func TestPendingReviewKeepsBodylessChangesRequested(t *testing.T) {
	f := fakeReviewForge{reviews: []tracker.Review{
		{Comment: tracker.Comment{ID: "1", Author: "rev", CreatedAt: at(1)}, State: tracker.ReviewChangesRequested},
		{Comment: tracker.Comment{ID: "2", Author: "rev", CreatedAt: at(2)}, State: tracker.ReviewCommented},
	}}
	b, err := PendingReview(context.Background(), f, reviewSlot(), nil)
	if err != nil || len(b.Items) != 1 || b.Items[0].State != tracker.ReviewChangesRequested {
		t.Fatalf("%+v %v", b.Items, err)
	}
}
