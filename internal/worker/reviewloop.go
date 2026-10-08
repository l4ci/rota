package worker

import (
	"context"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/tracker"
)

// ReviewForge is the part of the tracker adapter the review poll reads.
type ReviewForge interface {
	MRNotes(ctx context.Context, number int) ([]tracker.Comment, error)
	Reviews(ctx context.Context, number int) ([]tracker.Review, error)
}

// ReviewVerdict is a FAIL verdict recorded for the slot's branch.
type ReviewVerdict struct {
	At   time.Time
	Text string
}

// ReviewBatch is the review input waiting on a done slot's PR.
type ReviewBatch struct {
	Slot, PR string
	Items    []tracker.Review
	Verdict  string
	// Cursor is what Slot.SetReviewSeen takes once the batch is consumed.
	Cursor string
}

// Empty reports whether nothing is waiting.
func (b ReviewBatch) Empty() bool { return len(b.Items) == 0 && b.Verdict == "" }

// PendingReview reads the review input of a slot's PR that is newer than the
// slot's cursor: PR conversation comments, submitted reviews and inline diff
// comments, plus the FAIL verdict v when it is newer. It drops what is not
// review input:
//
//   - comments carrying a rota marker (the worker's `worker reply`, escalation
//     comments, claim and feedback notes). The marker, not the author, tells the
//     worker's voice apart: a solo maintainer runs the worker and reviews under
//     one forge login;
//   - approvals, and entries with no text.
//
// It reads and never writes: the cursor moves only when the batch is consumed
// (SetReviewSeen), so a restarted watch reports an unrelayed batch again.
// A slot with no PR has nothing to read.
func PendingReview(ctx context.Context, f ReviewForge, s *Slot, v *ReviewVerdict) (ReviewBatch, error) {
	b := ReviewBatch{Slot: s.Name(), PR: s.PR()}
	n, ok := PRRefNumber(s.PR())
	if !ok {
		return b, nil
	}
	after, _ := time.Parse(time.RFC3339Nano, s.ReviewSeen())
	notes, err := f.MRNotes(ctx, n)
	if err != nil {
		return b, err
	}
	reviews, err := f.Reviews(ctx, n)
	if err != nil {
		return b, err
	}
	all := make([]tracker.Review, 0, len(notes)+len(reviews))
	for _, c := range notes {
		all = append(all, tracker.Review{Comment: c, State: tracker.ReviewCommented})
	}
	all = append(all, reviews...)
	sort.SliceStable(all, func(i, j int) bool { return all[i].CreatedAt.Before(all[j].CreatedAt) })
	var newest time.Time
	for _, r := range all {
		switch {
		case r.State == tracker.ReviewApproved, strings.TrimSpace(r.Body) == "", marker.Has(r.Body):
			continue
		case !after.IsZero() && !r.CreatedAt.After(after):
			continue
		case r.CreatedAt.IsZero() && !after.IsZero():
			continue
		}
		b.Items = append(b.Items, r)
		if r.CreatedAt.After(newest) {
			newest = r.CreatedAt
		}
	}
	if v != nil && v.Text != "" && (after.IsZero() || v.At.After(after)) {
		b.Verdict = v.Text
		if v.At.After(newest) {
			newest = v.At
		}
	}
	if !b.Empty() {
		if newest.IsZero() { // a forge that dates nothing: from now on is new
			newest = time.Now()
		}
		b.Cursor = newest.UTC().Format(time.RFC3339Nano)
	}
	return b, nil
}

// Authors lists who the batch's items came from, first appearance first.
func (b ReviewBatch) Authors() []string {
	var out []string
	for _, it := range b.Items {
		if it.Author != "" && !slices.Contains(out, it.Author) {
			out = append(out, it.Author)
		}
	}
	return out
}

// ReviewSeen is the cursor of the review poll: the time of the newest review
// input already relayed to the worker ("" when none was).
func (s *Slot) ReviewSeen() string { return jsonx.Str(s.o, "reviewSeen") }

// SetReviewSeen moves the cursor; "" clears it.
func (s *Slot) SetReviewSeen(c string) {
	if c == "" {
		s.o.Delete("reviewSeen")
		return
	}
	s.o.Set("reviewSeen", c)
}
