package cli

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// AC-2: `round assign` on an It's a Plan key claims by a plain comment (no
// marker), labels and assigns the issue, and branches from the key.
func TestItsaplanRoundAssignClaims(t *testing.T) {
	fx := newScopeFx(t, "warn", "")
	s := itsaplanFake(t)
	deps, _ := itsaplanProject(t, s)
	fx.deps.TrackerOptions = append(fx.deps.TrackerOptions, deps.TrackerOptions...)
	write(t, filepath.Join(fx.root, ".rota", "config.json"), `{"forge":{"provider":"github"},"issues":{"source":"itsaplan","connection":"plan","itsaplan":{"project":"MKT"}},"round":{"scope":"open"}}`)
	wt := worktreeOf(t, fx.root, "park/dana")
	write(t, filepath.Join(fx.root, ".rota", "workers.json"),
		`{"architectureReview":{"at":"2020-01-01T00:00:00Z"},"slots":[{"name":"dana","branch":"park/dana","base":"feat/x","worktree":"`+wt+`"}]}`)
	cfg := mustJSONCfg(t, `{"issues":{"source":"itsaplan","connection":"plan","itsaplan":{"project":"MKT"}}}`)
	it, err := tracker.NewIssueTrackerFromConfig(context.Background(), cfg, fx.root, fx.deps.TrackerOptions...)
	if err != nil {
		t.Fatal(err)
	}
	key, err := it.Create(context.Background(), "Widen the seam", "## Acceptance\n- [ ] works\n", nil, "")
	if err != nil || key != "MKT-1" {
		t.Fatal(key, err)
	}

	code, out, errOut := rotaInWith(t, fx.deps, fx.root, "--json", "round", "assign", "MKT-1", "--agent", "dana", "--holder-pid", "4242")
	if code != 0 {
		t.Fatalf("assign: %d %s %s", code, out, errOut)
	}
	if b, _ := data(t, out)["branch"].(string); !strings.HasPrefix(b, "dana/MKT-1-") {
		t.Errorf("branch %q", b)
	}
	if task := worker.LoadRegistryTolerant(fx.root).Slot("dana").Task(); task != "MKT-1" {
		t.Errorf("slot task %q", task)
	}
	if cs, err := it.(tracker.ClaimStore).Claims(context.Background(), "MKT-1"); err != nil || !slices.Equal(cs, []string{"dana@0"}) {
		t.Errorf("claims %v, %v; requests %v", cs, err, s.Requests())
	}
	if !slices.Contains(s.Labels("MKT-1"), "in-progress") || !s.Assigned("MKT-1") {
		t.Errorf("labels %v, assigned %v", s.Labels("MKT-1"), s.Assigned("MKT-1"))
	}
	claims := 0
	for _, c := range s.CommentBodies("MKT-1") {
		if strings.HasPrefix(c, "Claimed by ") {
			claims++
			if strings.Contains(c, "<!--") {
				t.Errorf("a claim carries a marker: %q", c)
			}
		}
	}
	if claims != 1 {
		t.Errorf("%d claim comments, want 1: %q", claims, s.CommentBodies("MKT-1"))
	}
}
