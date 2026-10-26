package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/worker"
)

// A worker slot the round routed to a milestone branch reads that branch as
// its base; any other caller, and a slot on the round base, reads the repo's.
func TestGitBaseHonoursASlotRoutedToAMilestone(t *testing.T) {
	t.Setenv("ROTA_SLOT", "ben")
	d := t.TempDir()
	repo := newRepo(t, d, "repo", "main")
	slot := worker.NewSlot("ben", "ben/1-x", filepath.Join(repo, ".worktrees", "ben"), "main", "")
	if err := worker.Update(repo, func(doc *worker.Doc) { doc.SetSlots([]*worker.Slot{slot}) }); err != nil {
		t.Fatal(err)
	}
	asSlot := func() *Deps {
		deps := testDeps()
		deps.Getenv = func(k string) string {
			if k == "ROTA_SLOT" {
				return "ben"
			}
			return ""
		}
		return deps
	}
	base := func(deps *Deps) string {
		code, out, errOut := rotaInWith(t, deps, repo, "git", "base")
		if code != 0 {
			t.Fatalf("exit %d %s %s", code, out, errOut)
		}
		return strings.TrimSpace(out)
	}
	if got := base(asSlot()); got != "main" {
		t.Errorf("slot on the round base: %q", got)
	}
	slot.RouteBase("milestone/M01")
	if err := worker.Update(repo, func(doc *worker.Doc) { doc.SetSlots([]*worker.Slot{slot}) }); err != nil {
		t.Fatal(err)
	}
	if got := base(asSlot()); got != "milestone/M01" {
		t.Errorf("slot routed to a milestone: %q", got)
	}
	orchestrator := testDeps()
	orchestrator.Getenv = func(string) string { return "" }
	if got := base(orchestrator); got != "main" {
		t.Errorf("the orchestrator is not a slot: %q", got)
	}
}
