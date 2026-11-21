package round

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

const smokeBody = "## Acceptance\n- [ ] AC-1: adds a smoke section\n\nedits internal/%s.go"

// commitSections commits empty section files NN_x.sh on the checked-out
// branch of dir.
func commitSections(t *testing.T, dir string, nums ...string) {
	t.Helper()
	commitSectionsIn(t, dir, "test/sections", nums...)
}

// commitSectionsIn is commitSections for a section directory other than test/sections.
func commitSectionsIn(t *testing.T, dir, sub string, nums ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range nums {
		if err := os.WriteFile(filepath.Join(dir, sub, n+"_x.sh"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gittest.Run(t, dir, "add", sub)
	gittest.Run(t, dir, "commit", "-q", "-m", "sections")
}

func smokeFixture(t *testing.T) *assignFixture {
	t.Helper()
	f := newAssignFixture(t)
	f.env.Round.SmokeDir = "test/sections"
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
	if MentionsSmokeSection("## Acceptance\n- [ ] ok", "test/sections") || !MentionsSmokeSection("adds a Smoke-section", "test/sections") || !MentionsSmokeSection("edits test/sections/9_x.sh", "test/sections") {
		t.Error("only an issue that names a smoke section or the configured directory asks for one")
	}
	if MentionsSmokeSection("edits test/sections/9_x.sh", "t/smoke") || !MentionsSmokeSection("edits t/smoke/9_x.sh", "t/smoke") {
		t.Error("the directory comes from config, not a constant")
	}
	if MentionsSmokeSection("adds a smoke section", "") {
		t.Error("an empty directory disables the mention")
	}
}

// A project whose sections live elsewhere reserves a number there.
func TestAssignReservesInConfiguredSmokeDir(t *testing.T) {
	f := smokeFixture(t)
	f.env.Round.SmokeDir = "t/smoke"
	commitSectionsIn(t, f.root, "t/smoke", "007", "041")
	commitSections(t, f.root, "900")
	res, err := f.assign("15", "ben", nil)
	if err != nil || res.SmokeSection != 42 {
		t.Fatalf("want 42 from t/smoke, ignoring test/sections: %v %+v", err, res)
	}
	if !strings.Contains(f.host.sent, "t/smoke/42_*.sh") {
		t.Errorf("the brief names the configured directory:\n%s", f.host.sent)
	}
}

// With the key unset a project reserves nothing, even where test/sections exists.
func TestAssignSmokeDirUnsetReservesNone(t *testing.T) {
	f := smokeFixture(t)
	f.env.Round.SmokeDir = ""
	commitSections(t, f.root, "125")
	res, err := f.assign("15", "ben", nil)
	if err != nil || res.SmokeSection != 0 {
		t.Fatalf("unset key reserves 0: %v %+v", err, res)
	}
}

// An open PR's branch holds numbers the base does not have yet, and stays
// counted after the slot that opened it moved on.
func TestAssignCountsOpenPRBranches(t *testing.T) {
	f := smokeFixture(t)
	commitSections(t, f.root, "125")
	gittest.Run(t, f.root, "branch", "kit/99-other")
	gittest.Run(t, f.root, "worktree", "add", "-q", filepath.Join(f.root, ".worktrees", "pr"), "kit/99-other")
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
