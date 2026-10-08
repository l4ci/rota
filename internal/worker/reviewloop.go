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
	after, seen := parseCursor(s.ReviewSeen())
	notes, err := f.MRNotes(ctx, n)
	if err != nil {
		return b, err
	}
	reviews, err := f.Reviews(ctx, n)
	if err != nil {
		return b, err
	}
	type entry struct {
		r   tracker.Review
		key string // names the item within its kind: the kinds draw ids from separate sequences
	}
	all := make([]entry, 0, len(notes)+len(reviews))
	for _, c := range notes {
		all = append(all, entry{tracker.Review{Comment: c, State: tracker.ReviewCommented}, "n" + c.ID})
	}
	for _, r := range reviews {
		k := "r" + r.ID
		if r.Inline {
			k = "i" + r.ID
		}
		all = append(all, entry{r, k})
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].r.CreatedAt.Before(all[j].r.CreatedAt) })
	var newest time.Time
	var keys []string // the consumed keys created at newest
	take := func(at time.Time, key string) {
		switch {
		case at.After(newest):
			newest, keys = at, nil
			fallthrough
		case at.Equal(newest) && key != "":
			keys = append(keys, key)
		}
	}
	for _, e := range all {
		r := e.r
		bodyless := strings.TrimSpace(r.Body) == ""
		switch {
		case r.State == tracker.ReviewApproved, marker.Has(r.Body):
			continue
		case bodyless && r.State != tracker.ReviewChangesRequested:
			continue
		case !after.IsZero() && r.CreatedAt.Before(after):
			continue
		case !after.IsZero() && r.CreatedAt.Equal(after) && slices.Contains(seen, e.key):
			continue
		case r.CreatedAt.IsZero() && !after.IsZero():
			continue
		}
		b.Items = append(b.Items, r)
		take(r.CreatedAt, e.key)
	}
	if v != nil && v.Text != "" && (after.IsZero() || v.At.After(after)) {
		b.Verdict = v.Text
		take(v.At, "")
	}
	if !b.Empty() {
		if newest.IsZero() { // a forge that dates nothing: from now on is new
			newest = time.Now()
		}
		// An item at exactly the old cursor time that was already consumed stays consumed.
		if newest.Equal(after) {
			keys = append(slices.Clone(seen), keys...)
		}
		b.Cursor = formatCursor(newest, keys)
	}
	return b, nil
}

// The cursor is the newest consumed item's time, then "#" and the keys of the
// consumed items created at exactly that time. A forge dates to the second, so
// the time alone would drop an item that lands in the same second as the last
// consumed one.
func parseCursor(c string) (time.Time, []string) {
	ts, keys, _ := strings.Cut(c, "#")
	t, _ := time.Parse(time.RFC3339Nano, ts)
	if keys == "" {
		return t, nil
	}
	return t, strings.Split(keys, ",")
}

func formatCursor(newest time.Time, keys []string) string {
	c := newest.UTC().Format(time.RFC3339Nano)
	if len(keys) > 0 {
		c += "#" + strings.Join(keys, ",")
	}
	return c
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

// BaselineReview starts the review cursor at now when the slot hands its PR
// back and has none: whatever the PR collected before (bot posts, comments on
// an earlier push) is not review input. The second after now is skipped too,
// since a forge dates to the second. A slot that already has a cursor (a relay
// moved it) keeps it, so input that arrived while the worker reworked stays new.
func (s *Slot) BaselineReview(now time.Time) {
	if s.ReviewSeen() == "" {
		s.SetReviewSeen(formatCursor(now.Truncate(time.Second).Add(time.Second), nil))
	}
}
