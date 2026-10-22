package round

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/worker"
)

// AC-2: assign claims, branches and records the slot's task by key, and the
// overlap check holds a second key against the first.
func TestAssignKeyedIssuesClaimBranchAndOverlap(t *testing.T) {
	f := newAssignFixture(t)
	os.MkdirAll(filepath.Join(f.root, "internal", "cli"), 0o755)
	os.WriteFile(filepath.Join(f.root, "internal", "cli", "round.go"), []byte("x"), 0o644)
	sh(t, f.root, "add", "internal/cli/round.go")
	sh(t, f.root, "commit", "-q", "-m", "round.go")
	f.be.add("PROJ-123", "Widen the tracker seam", "M01", false, "## Acceptance\n- [ ] works\n\nedits internal/cli/round.go")
	f.be.add("PROJ-124", "Another key", "M01", false, "## Acceptance\n- [ ] ok\n\nalso edits internal/cli/round.go")

	res, err := f.assign("PROJ-123", "ben", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Branch != "ben/PROJ-123-widen-the-tracker-seam" || !res.Dispatched {
		t.Fatalf("%+v", res)
	}
	if f.be.claims["PROJ-123"] != "ben@1" {
		t.Errorf("claim = %+v", f.be.claims)
	}
	if s := worker.LoadRegistryTolerant(f.root).Slot("ben"); s.Task() != "PROJ-123" || s.HeldID() != "PROJ-123" {
		t.Errorf("slot task %q held %q", s.Task(), s.HeldID())
	}
	if !strings.Contains(f.host.sent, "issue PROJ-123") || !strings.Contains(f.host.sent, "ben/PROJ-123-") {
		t.Errorf("brief names the key and branch:\n%s", f.host.sent)
	}

	_, err = f.assign("PROJ-124", "dana", nil)
	if by := blockedBy(t, err); by != BlockOverlap {
		t.Fatalf("PROJ-124 overlaps PROJ-123: %v", err)
	}
}

// A key-based tracker's Depends on line can name PROJ-12 of its own project;
// prose (UTF-8, SHA-256, ISO-8601) and other projects are not refs.
func TestDependenciesReadKeysOnlyOnAKeyedTracker(t *testing.T) {
	text := "## Depends on\n- PROJ-12, OTHER-3 and #7 (needs UTF-8, SHA-256, ISO-8601)\n"
	if refs, _ := dependencies(text, "PROJ"); strings.Join(refs, ",") != "7,PROJ-12" {
		t.Errorf("keyed refs = %v", refs)
	}
	if refs, _ := dependencies(text, ""); strings.Join(refs, ",") != "7" {
		t.Errorf("numbered refs = %v, want just 7", refs)
	}
}

// On a key-based tracker only keys of the item's own project are refs: prose
// like UTF-8 in a Depends on section names no issue and blocks nothing.
func TestAssignKeyedDependsOnReadsOnlyItsProject(t *testing.T) {
	f := newAssignFixture(t)
	f.be.add("PROJ-7", "Done first", "M01", true, "")
	f.be.add("PROJ-125", "Encode names", "M01", false, "## Acceptance\n- [ ] ok\n\n## Depends on\n- PROJ-7, stored as UTF-8 per ISO-8601\n")
	if _, err := f.assign("PROJ-125", "ben", nil); err != nil {
		t.Fatalf("prose blocked the assign: %v", err)
	}
}

// A list item in Depends on that starts with another project's key names a
// dependency rota cannot read: the item is not ready.
func TestAssignKeyedDependsOnAnotherProjectIsUnverifiable(t *testing.T) {
	text := "## Depends on\n- OTHER-3\n* ops-9 rollout\n- stored as UTF-8\n- PROJ-12, OTHER-4 too\n"
	refs, unverifiable := dependencies(text, "PROJ")
	if strings.Join(refs, ",") != "PROJ-12" || strings.Join(unverifiable, ",") != "OTHER-3,OPS-9" {
		t.Fatalf("refs %v, unverifiable %v", refs, unverifiable)
	}
	f := newAssignFixture(t)
	f.be.add("PROJ-126", "Cross", "M01", false, "## Acceptance\n- [ ] ok\n\n## Depends on\n- OTHER-3\n")
	_, err := f.assign("PROJ-126", "ben", nil)
	if err == nil || !strings.Contains(err.Error(), "cannot verify OTHER-3") || !strings.Contains(err.Error(), "PROJ-N") {
		t.Fatalf("another project's dependency passed: %v", err)
	}
}
