package worker

import (
	"context"
	"testing"

	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/verdict"
)

// TestPendingReviewIgnoresVerdictReports pins #3162: the report `rota verdict
// add` posts on the PR is rota's own comment, keyed by its marker and not by
// who posted it, so it is never review input; a human comment still is.
func TestPendingReviewIgnoresVerdictReports(t *testing.T) {
	rep := verdict.RenderReport(verdict.Record{Kind: "review", Sha: "abc1234", Verdict: "FAIL", Summary: "nope"}, nil)
	f := fakeReviewForge{notes: []tracker.Comment{
		{ID: "1", Author: "me", Body: rep, CreatedAt: at(1)},
		{ID: "2", Author: "me", Body: marker.Report("gate", "abc1234") + "\n### Gate report\n", CreatedAt: at(2)},
	}}
	b, err := PendingReview(context.Background(), f, reviewSlot(), nil, testNow)
	if err != nil || len(b.Items) != 0 {
		t.Fatalf("verdict reports read as review input: %+v %v", b.Items, err)
	}
	f.notes = append(f.notes, tracker.Comment{ID: "3", Author: "me", Body: "please rename x", CreatedAt: at(3)})
	b, _ = PendingReview(context.Background(), f, reviewSlot(), nil, testNow)
	if len(b.Items) != 1 || b.Items[0].ID != "3" {
		t.Fatalf("human comment: %+v", b.Items)
	}
}
