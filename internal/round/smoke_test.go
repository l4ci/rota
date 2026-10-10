package round

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

const smokeBody = "## Acceptance\n- [ ] AC-1: adds a smoke section\n\nedits internal/%s.go"

// commitSections commits empty section files NN_x.sh on the checked-out
// branch of dir.
func commitSections(t *testing.T, dir string, nums ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "test", "sections"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range nums {
		if err := os.WriteFile(filepath.Join(dir, "test", "sections", n+"_x.sh"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sh(t, dir, "add", "test/sections")
	sh(t, dir, "commit", "-q", "-m", "sections")
}

func smokeFixture(t *testing.T) *assignFixture {
	t.Helper()
	f := newAssignFixture(t)
	f.be.add("15", "Smoke one", "M01", false, strings.Replace(smokeBody, "%s", "one", 1))
	f.be.add("16", "Smoke two", "M01", false, strings.Replace(smokeBody, "%s", "two", 1))
	return f
}

func TestAssignReservesDistinctSmokeSections(t *testing.T) {
	f := smokeFixture(t)
	commitSections(t, f.root, "001", "125")
	first, err := f.assign("15", "ben", nil)
	if err != nil || first.SmokeSection != 126 {
		t.Fatalf("first reservation: %v %+v", err, first)
	}
	if got := worker.LoadRegistryTolerant(f.root).Slot("ben").SmokeSection(); got != 126 {
		t.Errorf("the number is recorded on the slot: %d", got)
	}
	if !strings.Contains(f.host.sent, "number 126") {
		t.Errorf("the brief names the number:\n%s", f.host.sent)
	}
	second, err := f.assign("16", "dana", nil)
	if err != nil || second.SmokeSection != 127 {
		t.Fatalf("a second assign must not reuse 126: %v %+v", err, second)
	}
	if MentionsSmokeSection("## Acceptance\n- [ ] ok") || !MentionsSmokeSection("adds a Smoke-section") || !MentionsSmokeSection("edits test/sections/9_x.sh") {
		t.Error("only an issue that names a smoke section or test/sections asks for one")
	}
}

// An open PR's branch holds numbers the base does not have yet, and stays
// counted after the slot that opened it moved on.
func TestAssignCountsOpenPRBranches(t *testing.T) {
	f := smokeFixture(t)
	commitSections(t, f.root, "125")
	sh(t, f.root, "branch", "kit/99-other")
	sh(t, f.root, "worktree", "add", "-q", filepath.Join(f.root, ".worktrees", "pr"), "kit/99-other")
	commitSections(t, filepath.Join(f.root, ".worktrees", "pr"), "131")
	f.env.Forge = (&fakeRemote{prs: []tracker.PR{{Number: 7, Branch: "kit/99-other"}}}).asForge()
	res, err := f.assign("15", "ben", nil)
	if err != nil || res.SmokeSection != 132 {
		t.Fatalf("want 132 above the PR branch's 131: %v %+v", err, res)
	}
}

func TestAssignResumeKeepsTheSmokeSection(t *testing.T) {
	f := smokeFixture(t)
	commitSections(t, f.root, "125")
	if _, err := f.assign("15", "ben", nil); err != nil {
		t.Fatal(err)
	}
	again, err := f.assign("15", "ben", nil)
	if err != nil || again.SmokeSection != 126 {
		t.Fatalf("a resumed assign keeps its number: %v %+v", err, again)
	}
}

func TestAssignNoSectionsDirNoReservation(t *testing.T) {
	f := smokeFixture(t)
	res, err := f.assign("15", "ben", nil)
	if err != nil || res.SmokeSection != 0 {
		t.Fatalf("a project without test/sections gets none: %v %+v", err, res)
	}
	if strings.Contains(f.host.sent, "smoke section") {
		t.Errorf("no number in the brief:\n%s", f.host.sent)
	}
}
