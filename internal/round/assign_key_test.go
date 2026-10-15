package round

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/worker"
)

// A key-based tracker names issues PROJ-123: the branch carries the key as
// the tracker spells it, and the worker package reads it back.
func TestBranchNameKeyRoundTrips(t *testing.T) {
	br := BranchName("ben", "PROJ-123", "Widen issue identifiers to tracker keys")
	if br != "ben/PROJ-123-widen-issue-identifiers-to-tracker" {
		t.Fatalf("branch = %q", br)
	}
	if k, ok := worker.BranchIssue(br); !ok || k != "PROJ-123" {
		t.Errorf("BranchIssue = %q,%v", k, ok)
	}
	if got := worker.IssueFromBranch(br); got != "PROJ-123" {
		t.Errorf("IssueFromBranch = %q", got)
	}
	if got := worker.HeldID("", br, "ben"); got != "PROJ-123" {
		t.Errorf("HeldID = %q", got)
	}
	if !worker.RoundBranch(br) {
		t.Error("a key branch is a round branch")
	}
	// A lower-case slug that merely looks like a key is not an issue.
	if worker.RoundBranch("dana/add-12-things") {
		t.Error("add-12-things is a slug, not a key")
	}
}

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

// A Depends on line can name a key-based tracker's issue.
func TestDependenciesReadKeys(t *testing.T) {
	refs, _ := Dependencies("## Depends on\n- PROJ-12 and #7\n")
	if strings.Join(refs, ",") != "7,PROJ-12" {
		t.Errorf("refs = %v", refs)
	}
}
