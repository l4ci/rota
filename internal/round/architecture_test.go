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
)

type archForge struct {
	closed []tracker.Issue
	added  map[int][]string
}

func (archForge) OpenPRs(context.Context) ([]tracker.PR, error) { return nil, nil }
func (archForge) PRState(context.Context, int) (string, error)  { return "", nil }
func (f archForge) List(context.Context, tracker.ListFilter) ([]tracker.Issue, error) {
	return f.closed, nil
}
func (f archForge) AddLabels(_ context.Context, n int, l []string, _ bool) error {
	f.added[n] = l
	return nil
}

type archBacklog struct {
	backlog.Backend
	open []backlog.Item
	made []backlog.CreateInput
}

func (b *archBacklog) Name() string                      { return "issues" }
func (b *archBacklog) List(bool) ([]backlog.Item, error) { return b.open, nil }
func (b *archBacklog) Get(ref string) (*backlog.Item, error) {
	for i := range b.open {
		if b.open[i].ID == ref {
			return &b.open[i], nil
		}
	}
	return nil, backlog.ErrNotFound
}
func (b *archBacklog) Create(in backlog.CreateInput) (backlog.CreateResult, error) {
	b.made = append(b.made, in)
	n := 100 + len(b.made)
	id := fmt.Sprint(n)
	b.open = append(b.open, backlog.Item{ID: id, Number: n, Title: in.Title})
	return backlog.CreateResult{ID: id, Type: "T"}, nil
}

func archFixture(t *testing.T, every int, closed []tracker.Issue) (string, Env, *archBacklog, roundcfg.Settings) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeRegistry(t, root, slot(root, "ben", "park/ben", nil))
	e := env(nil, archForge{closed: closed, added: map[int][]string{}})
	e.Now = func() time.Time { return time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC) }
	set := roundcfg.Settings{Roster: []string{"ben"}, ArchitectureEvery: every, ArchitectureAreas: []string{"cli", "worker"}}
	return root, e, &archBacklog{}, set
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
	if f := e.Forge.(archForge); len(f.added) != 2 || f.added[101][0] != RefactorLabel {
		t.Fatalf("review items should carry the refactor label: %v", f.added)
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
	be.open = nil
	e.Forge = archForge{closed: append(closed, closedIssue(3, "c", "2026-10-05T00:00:00Z")), added: map[int][]string{}}
	a, _ = e.Architecture(ctx, root, be, set, []Candidate{{ID: "9"}})
	if a.Count != 1 || a.Due {
		t.Fatalf("count should restart after the review: %+v", a)
	}
}

func TestArchitectureQueueEmptyNeedsIdleSlotNoReadyCandidateAndNewWork(t *testing.T) {
	root, e, be, set := archFixture(t, 20, []tracker.Issue{closedIssue(1, "a", "2026-10-01T00:00:00Z")})
	ctx := context.Background()
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
	e.Forge = archForge{added: map[int][]string{}}
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
	if err != nil || len(got) != 2 {
		t.Fatalf("slate scope should still offer the review item: %v %v", got, err)
	}
}
