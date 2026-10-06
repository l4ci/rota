package round

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

func archFixture(t *testing.T, every int, closed []tracker.Issue) (string, Env, *fakeRemote, roundcfg.Settings) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeRegistry(t, root, slot(root, "ben", "park/ben", nil))
	r := &fakeRemote{closedIssues: closed}
	e := env(nil, r.asForge())
	e.Now = func() time.Time { return time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC) }
	set := roundcfg.Settings{Roster: []string{"ben"}, ArchitectureEvery: every, ArchitectureAreas: []string{"cli", "worker"}}
	return root, e, r, set
}

// seedReview records a past review so closed issues after it count.
func seedReview(t *testing.T, root string) {
	t.Helper()
	err := worker.Update(root, func(d *worker.Doc) { d.StartReview("2026-09-30T00:00:00Z", 1, "test", nil) })
	if err != nil {
		t.Fatal(err)
	}
}

func closedIssue(n int, title, at string, labels ...string) tracker.Issue {
	return tracker.Issue{Number: n, Title: title, State: "closed", ClosedAt: at, Labels: labels, StateReason: "completed"}
}

func TestArchitectureCountSkipsRefactorWork(t *testing.T) {
	root, e, be, set := archFixture(t, 3, []tracker.Issue{
		closedIssue(1, "a feature", "2026-10-01T00:00:00Z"),
		closedIssue(2, "labelled", "2026-10-01T00:00:00Z", RefactorLabel),
		closedIssue(3, "arch(cli): split the verbs", "2026-10-01T00:00:00Z"),
		{Number: 4, Title: "dropped", State: "closed", StateReason: "not_planned", ClosedAt: "2026-10-01T00:00:00Z"},
		closedIssue(5, "another", "2026-10-02T00:00:00Z"),
	})
	seedReview(t, root)
	a, err := e.Architecture(context.Background(), root, be, set, []Candidate{{ID: "9"}})
	if err != nil {
		t.Fatal(err)
	}
	if a.Count != 2 || a.Until != 1 || a.Due {
		t.Fatalf("count %d until %d due %v, want 2 1 false", a.Count, a.Until, a.Due)
	}
	if got := a.Line(); got != "architecture review in 1 issues" {
		t.Fatalf("line %q", got)
	}
}

func TestArchitectureThresholdMintsPerAreaAndRestartsCount(t *testing.T) {
	closed := []tracker.Issue{closedIssue(1, "a", "2026-10-01T00:00:00Z"), closedIssue(2, "b", "2026-10-02T00:00:00Z")}
	root, e, be, set := archFixture(t, 2, closed)
	ctx := context.Background()
	seedReview(t, root)
	a, _ := e.Architecture(ctx, root, be, set, []Candidate{{ID: "9"}})
	if !a.Due || a.Trigger != TriggerThreshold {
		t.Fatalf("want a threshold review, got %+v", a)
	}
	ids, err := e.MintReview(ctx, root, be, a, 3)
	if err != nil || len(ids) != 2 {
		t.Fatalf("ids %v err %v", ids, err)
	}
	if be.made[0].Title != "arch(cli): architecture review" || be.made[1].Title != "arch(worker): architecture review" {
		t.Fatalf("titles %q %q", be.made[0].Title, be.made[1].Title)
	}
	if len(be.labels) != 2 || be.labels[101][0] != RefactorLabel {
		t.Fatalf("review items should carry the refactor label: %v", be.labels)
	}
	if got := ReviewSince(root); got != "2026-10-04T12:00:00Z" {
		t.Fatalf("since %q", got)
	}
	// The review is in flight: nothing more is due, however many have closed.
	a, _ = e.Architecture(ctx, root, be, set, nil)
	if a.Due || len(a.Pending) != 2 {
		t.Fatalf("a review in flight should not retrigger: %+v", a)
	}
	// Once the review items close, only items closed after it count.
	be.items, be.order = nil, nil
	e.Now = func() time.Time { return time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC) } // past the cache
	be.closedIssues = append(closed, closedIssue(3, "c", "2026-10-05T00:00:00Z"))
	a, _ = e.Architecture(ctx, root, be, set, []Candidate{{ID: "9"}})
	if a.Count != 1 || a.Due {
		t.Fatalf("count should restart after the review: %+v", a)
	}
}

func TestArchitectureQueueEmptyNeedsIdleSlotNoReadyCandidateAndNewWork(t *testing.T) {
	root, e, be, set := archFixture(t, 20, []tracker.Issue{closedIssue(1, "a", "2026-10-01T00:00:00Z")})
	ctx := context.Background()
	seedReview(t, root)
	notReady := []Candidate{{ID: "7", Readiness: Readiness{Checks: []Check{{Name: "criteria", OK: false}}}}}
	a, _ := e.Architecture(ctx, root, be, set, notReady)
	if !a.Due || a.Trigger != TriggerQueueEmpty || a.Idle != 1 {
		t.Fatalf("an idle slot with nothing ready should trigger: %+v", a)
	}
	if a, _ = e.Architecture(ctx, root, be, set, []Candidate{{ID: "7"}}); a.Due {
		t.Fatal("a ready candidate keeps the queue non-empty")
	}
	// A slot that holds an issue is not idle.
	writeRegistry(t, root, slot(root, "ben", "ben/5-x", nil))
	if a, _ = e.Architecture(ctx, root, be, set, nil); a.Due {
		t.Fatal("no idle slot, no review")
	}
	// Nothing closed since the last review: an empty queue has nothing to review.
	writeRegistry(t, root, slot(root, "ben", "park/ben", nil))
	be.closedIssues = nil
	e.Now = func() time.Time { return time.Date(2026, 10, 4, 13, 0, 0, 0, time.UTC) } // past the cache
	if a, _ = e.Architecture(ctx, root, be, set, nil); a.Due {
		t.Fatal("an empty queue with no new closed work must not loop reviews")
	}
}

func TestArchitectureOffAndAreaFallback(t *testing.T) {
	root, e, be, set := archFixture(t, 0, []tracker.Issue{closedIssue(1, "a", "2026-10-01T00:00:00Z")})
	if a, _ := e.Architecture(context.Background(), root, be, set, nil); a.Due || a.Line() != "" {
		t.Fatalf("every 0 is off: %+v", a)
	}
	set.ArchitectureAreas = nil
	if got := reviewAreas(root, set); len(got) != 1 || got[0] != WholeRepo {
		t.Fatalf("no map, no config: %v", got)
	}
	if err := os.MkdirAll(filepath.Join(root, ".rota", "map"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".rota", "map", "claims.md"), []byte("---\nsubsystem: claims\n---\n# claims\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := reviewAreas(root, set); len(got) != 1 || got[0] != "claims" {
		t.Fatalf("subsystem map names: %v", got)
	}
}

func TestReviewItemsAreInEveryScope(t *testing.T) {
	root := t.TempDir()
	items := []backlog.Item{{ID: "1", Title: "plain"}, {ID: "2", Title: ReviewTitle("cli")}}
	got, err := scopeSet(root, items, map[string]bool{}, roundcfg.ScopeSlate, []string{"1"})
	if err != nil || len(got) != 1 {
		t.Fatalf("a title alone must not bypass the scope: %v %v", got, err)
	}
	writeRegistry(t, root)
	if err := worker.Update(root, func(d *worker.Doc) { d.RecordReviewItems([]string{"2"}) }); err != nil {
		t.Fatal(err)
	}
	got, err = scopeSet(root, items, map[string]bool{}, roundcfg.ScopeSlate, []string{"1"})
	if err != nil || len(got) != 2 {
		t.Fatalf("a minted review item should be in every scope: %v %v", got, err)
	}
}

func TestArchitectureFirstSightSeedsInsteadOfTriggering(t *testing.T) {
	closed := []tracker.Issue{closedIssue(1, "a", "2026-10-01T00:00:00Z"), closedIssue(2, "b", "2026-10-02T00:00:00Z")}
	root, e, be, set := archFixture(t, 2, closed)
	a, err := e.Architecture(context.Background(), root, be, set, []Candidate{{ID: "9"}})
	if err != nil {
		t.Fatal(err)
	}
	if a.Count != 0 || a.Due || a.Until != 2 || a.Since != "2026-10-04T12:00:00Z" {
		t.Fatalf("the first run should seed, not trigger: %+v", a)
	}
	if got := ReviewSince(root); got != a.Since {
		t.Fatalf("seed not saved: %q", got)
	}
	if !worker.LoadRegistry(root).Review().Seeded {
		t.Fatal("the seeded timestamp should be marked unratified")
	}
}

func TestArchitectureCachesClosedCount(t *testing.T) {
	root, e, be, set := archFixture(t, 5, []tracker.Issue{closedIssue(1, "a", "2026-10-01T00:00:00Z")})
	seedReview(t, root)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if a, _ := e.Architecture(ctx, root, be, set, []Candidate{{ID: "9"}}); a.Count != 1 {
			t.Fatalf("count %d", a.Count)
		}
	}
	if be.closedLists != 1 {
		t.Fatalf("closed issues fetched %d times, want 1", be.closedLists)
	}
	e.Now = func() time.Time { return time.Date(2026, 10, 4, 12, 10, 0, 0, time.UTC) }
	e.Architecture(ctx, root, be, set, []Candidate{{ID: "9"}})
	if be.closedLists != 2 {
		t.Fatalf("an expired cache should refetch, fetched %d times", be.closedLists)
	}
}

func TestMintReviewRecordsCreatedItemsOnError(t *testing.T) {
	root, e, be, set := archFixture(t, 1, []tracker.Issue{closedIssue(1, "a", "2026-10-01T00:00:00Z")})
	seedReview(t, root)
	ctx := context.Background()
	a, _ := e.Architecture(ctx, root, be, set, []Candidate{{ID: "9"}})
	be.addErr = fmt.Errorf("label boom")
	if _, err := e.MintReview(ctx, root, be, a, 1); err == nil {
		t.Fatal("want the label error")
	}
	if got := MintedReviews(root); !got["101"] {
		t.Fatalf("the created item should be recognised after the error: %v", got)
	}
	if a, _ = e.Architecture(ctx, root, be, set, nil); len(a.Pending) != 1 || a.Due {
		t.Fatalf("the created item should show as a review in flight, not be re-minted: %+v", a)
	}
}
