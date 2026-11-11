package round

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// integrationFixture is milestoneFixture with integration/r1-a on origin, one
// commit ahead of main (the file "integration-only"), and item 15, tagged to
// the active milestone M01, recorded as its interaction item.
func integrationFixture(t *testing.T) *assignFixture {
	t.Helper()
	f, _ := milestoneFixture(t)
	gittest.Run(t, f.root, "switch", "-q", "-c", "int-tmp", "main")
	writeFile(t, filepath.Join(f.root, "integration-only"), "i")
	gittest.Run(t, f.root, "add", "integration-only")
	gittest.Run(t, f.root, "commit", "-q", "-m", "integration")
	gittest.Run(t, f.root, "push", "-q", "origin", "int-tmp:refs/heads/integration/r1-a")
	gittest.Run(t, f.root, "switch", "-q", "main")
	gittest.Run(t, f.root, "branch", "-q", "-D", "int-tmp")
	if err := worker.RecordIntegration(f.root, worker.Integration{Name: "a", Branch: "integration/r1-a", Round: 1, Base: "main", Items: []string{"15"}}); err != nil {
		t.Fatal(err)
	}
	f.be.add("15", "Interaction on integration/r1-a", "M01", false, "## Acceptance\n- [ ] ok\n\nedits a.go")
	return f
}

func TestAssignIntegrationItemCutsFromIntegration(t *testing.T) {
	f := integrationFixture(t)
	res, err := f.assign("15", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if b := worker.LoadRegistryTolerant(f.root).Slot(res.Agent).Base(); b != "integration/r1-a" {
		t.Errorf("slot base = %q, want integration/r1-a", b)
	}
	if _, err := os.Stat(filepath.Join(f.root, ".worktrees", res.Agent, "integration-only")); err != nil {
		t.Errorf("the branch must be cut from the integration branch: %v", err)
	}
	// The integration branch wins over the item's milestone; any other item
	// keeps its own base.
	other, err := f.assign("12", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if b := worker.LoadRegistryTolerant(f.root).Slot(other.Agent).Base(); b != "milestone/M01" {
		t.Errorf("milestone item base = %q, want milestone/M01", b)
	}
}

func TestRetargetKeepsIntegrationBase(t *testing.T) {
	f := integrationFixture(t)
	res, err := f.assign("15", "ben", nil)
	if err != nil {
		t.Fatal(err)
	}
	const url = "https://github.com/o/r/pull/7"
	if err := editSlot(f.root, "ben", func(s *worker.Slot) error {
		s.SetPR(url)
		return s.MarkState("done", "")
	}); err != nil {
		t.Fatal(err)
	}
	f.be.prs = []tracker.PR{{Number: 7, Branch: res.Branch, URL: url}}
	f.env.Forge = f.be.asForge()
	f.env.Board = f.be
	// Leaving the milestone must not move its fix off the branch.
	f.be.items["15"].Fields.Milestone = ""
	out, err := f.env.Reconcile(bg, f.root, true)
	if err != nil {
		t.Fatal(err)
	}
	if n := countKind(out.Repaired, BaseDrift) + countKind(out.Drift, BaseDrift); n != 0 {
		t.Fatalf("an interaction fix was retargeted: %+v %+v", out.Repaired, out.Drift)
	}
	if b := worker.LoadRegistryTolerant(f.root).Slot("ben").Base(); b != "integration/r1-a" {
		t.Errorf("slot base = %q", b)
	}
}
