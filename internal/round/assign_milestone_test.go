package round

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// milestoneFixture is newAssignFixture with an origin that holds milestone/M01,
// one commit ahead of main (the file "milestone-only").
func milestoneFixture(t *testing.T) (*assignFixture, string) {
	t.Helper()
	f := newAssignFixture(t)
	origin := filepath.Join(gitTemp(t), "origin.git")
	gittest.Run(t, f.root, "clone", "-q", "--bare", f.root, origin)
	gittest.Run(t, f.root, "remote", "add", "origin", origin)
	gittest.Run(t, f.root, "fetch", "-q", "origin")
	gittest.Run(t, f.root, "switch", "-q", "-c", "ms-tmp", "main")
	writeFile(t, filepath.Join(f.root, "milestone-only"), "m")
	gittest.Run(t, f.root, "add", "milestone-only")
	gittest.Run(t, f.root, "commit", "-q", "-m", "milestone work")
	gittest.Run(t, f.root, "push", "-q", "origin", "ms-tmp:refs/heads/milestone/M01")
	gittest.Run(t, f.root, "switch", "-q", "main")
	gittest.Run(t, f.root, "branch", "-q", "-D", "ms-tmp")
	return f, origin
}

func TestAssignRoutesAnActiveMilestoneIssueToItsBranch(t *testing.T) {
	f, _ := milestoneFixture(t)
	res, err := f.assign("12", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	s := worker.LoadRegistryTolerant(f.root).Slot(res.Agent)
	if s.Base() != "milestone/M01" {
		t.Errorf("slot base = %q, want milestone/M01", s.Base())
	}
	if _, err := os.Stat(filepath.Join(f.root, ".worktrees", res.Agent, "milestone-only")); err != nil {
		t.Errorf("the branch must be cut from the milestone branch: %v", err)
	}
	if !strings.Contains(f.host.sent, "milestone/M01") {
		t.Errorf("brief must name the base:\n%s", f.host.sent)
	}
}

func TestAssignKeepsTheBaseForUntaggedAndPlannedMilestoneIssues(t *testing.T) {
	f, _ := milestoneFixture(t)
	milestoneDoc(t, f.root, "M02", "planned")
	if _, err := f.env.Start(bg, f.root, startOpts(roundcfg.ScopeOpen, 100)); err != nil {
		t.Fatal(err)
	}
	f.be.add("15", "Untagged issue for the base", "", false, "## Acceptance\n- [ ] ok\n\nedits a.go")
	f.be.add("16", "Planned milestone issue", "M02", false, "## Acceptance\n- [ ] ok\n\nedits b.go")
	for _, id := range []string{"15", "16"} {
		res, err := f.assign(id, "", nil)
		if err != nil {
			t.Fatal(id, err)
		}
		if b := worker.LoadRegistryTolerant(f.root).Slot(res.Agent).Base(); b != "main" {
			t.Errorf("%s: slot base = %q, want main", id, b)
		}
		if strings.Contains(f.host.sent, "milestone/") {
			t.Errorf("%s: brief must not name a milestone branch:\n%s", id, f.host.sent)
		}
	}
}

func TestAssignKeepsTheBaseWhenTheMilestoneBranchIsMissing(t *testing.T) {
	f, origin := milestoneFixture(t)
	gittest.Run(t, f.root, "push", "-q", origin, "--delete", "milestone/M01")
	res, err := f.assign("12", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if b := worker.LoadRegistryTolerant(f.root).Slot(res.Agent).Base(); b != "main" {
		t.Errorf("slot base = %q, want main", b)
	}
	if len(res.Warnings) == 0 || !strings.Contains(strings.Join(res.Warnings, "\n"), "milestone/M01") {
		t.Errorf("want a warning naming the missing branch: %v", res.Warnings)
	}
}

// pushMilestoneBranch cuts milestone/<id> on origin from main.
func pushMilestoneBranch(t *testing.T, f *assignFixture, id string) {
	t.Helper()
	gittest.Run(t, f.root, "push", "-q", "origin", "main:refs/heads/milestone/"+id)
}

func TestReconcileRetargetsAPRWhoseIssueChangedMilestone(t *testing.T) {
	f, _ := milestoneFixture(t)
	res, err := f.assign("12", "ben", nil)
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

	milestoneDoc(t, f.root, "M02", "active")
	pushMilestoneBranch(t, f, "M02")
	f.be.items["12"].Fields.Milestone = "M02"

	out, err := f.env.Reconcile(bg, f.root, true)
	if err != nil {
		t.Fatal(err)
	}
	if n := countKind(out.Repaired, BaseDrift); n != 1 {
		t.Fatalf("want one repaired base-drift, got %+v / drift %+v / warnings %v", out.Repaired, out.Drift, out.Report.Warnings)
	}
	if got := f.be.retargeted[7]; got != "milestone/M02" {
		t.Errorf("PR 7 retargeted onto %q, want milestone/M02", got)
	}
	s := worker.LoadRegistryTolerant(f.root).Slot("ben")
	if s.Base() != "milestone/M02" {
		t.Errorf("slot base = %q", s.Base())
	}
	if un := s.Unread(); len(un) != 1 || !strings.Contains(un[0].Text, "git merge origin/milestone/M02") {
		t.Errorf("the worker must be told to merge the new base: %+v", un)
	}
	// A second pass finds nothing left to repair.
	if again, _ := f.env.Reconcile(bg, f.root, true); countKind(again.Repaired, BaseDrift)+countKind(again.Drift, BaseDrift) != 0 {
		t.Errorf("second pass: %+v %+v", again.Repaired, again.Drift)
	}
}

func TestReconcileRetargetsBackWhenTheIssueLeavesItsMilestone(t *testing.T) {
	f, _ := milestoneFixture(t)
	res, err := f.assign("12", "ben", nil)
	if err != nil {
		t.Fatal(err)
	}
	const url = "https://github.com/o/r/pull/8"
	if err := editSlot(f.root, "ben", func(s *worker.Slot) error {
		s.SetPR(url)
		return s.MarkState("done", "")
	}); err != nil {
		t.Fatal(err)
	}
	f.be.prs = []tracker.PR{{Number: 8, Branch: res.Branch, URL: url}}
	f.env.Forge = f.be.asForge()
	f.env.Board = f.be
	f.be.items["12"].Fields.Milestone = ""

	if _, err := f.env.Reconcile(bg, f.root, true); err != nil {
		t.Fatal(err)
	}
	if got := f.be.retargeted[8]; got != "main" {
		t.Errorf("PR 8 retargeted onto %q, want main", got)
	}
	if b := worker.LoadRegistryTolerant(f.root).Slot("ben").Base(); b != "main" {
		t.Errorf("slot base = %q, want main", b)
	}
}

func countKind(fs []Finding, kind string) int {
	n := 0
	for _, f := range fs {
		if f.Kind == kind {
			n++
		}
	}
	return n
}

// doneWithPR parks slot ben as done on a PR for the branch it was assigned.
func doneWithPR(t *testing.T, f *assignFixture, branch string, n int) {
	t.Helper()
	url := "https://github.com/o/r/pull/" + strconv.Itoa(n)
	if err := editSlot(f.root, "ben", func(s *worker.Slot) error {
		s.SetPR(url)
		return s.MarkState("done", "")
	}); err != nil {
		t.Fatal(err)
	}
	f.be.prs = []tracker.PR{{Number: n, Branch: branch, URL: url}}
	f.env.Forge = f.be.asForge()
	f.env.Board = f.be
}

func TestReconcileIgnoresAMilestoneGoingActiveUnderAnUnchangedTag(t *testing.T) {
	f, _ := milestoneFixture(t)
	milestoneDoc(t, f.root, "M02", "planned")
	if _, err := f.env.Start(bg, f.root, startOpts(roundcfg.ScopeOpen, 100)); err != nil {
		t.Fatal(err)
	}
	f.be.add("16", "Planned milestone issue", "M02", false, "## Acceptance\n- [ ] ok\n\nedits b.go")
	res, err := f.assign("16", "ben", nil)
	if err != nil {
		t.Fatal(err)
	}
	doneWithPR(t, f, res.Branch, 9)
	milestoneDoc(t, f.root, "M02", "active")
	pushMilestoneBranch(t, f, "M02")

	for i := 0; i < 2; i++ {
		out, err := f.env.Reconcile(bg, f.root, true)
		if err != nil {
			t.Fatal(err)
		}
		if n := countKind(out.Repaired, BaseDrift) + countKind(out.Drift, BaseDrift); n != 0 {
			t.Fatalf("activation alone is not drift: %+v", out)
		}
	}
	if len(f.be.retargeted) != 0 {
		t.Errorf("PR retargeted: %v", f.be.retargeted)
	}
}

func TestReconcileHoldsTheSlotOutOfTheGateAfterARetarget(t *testing.T) {
	f, _ := milestoneFixture(t)
	res, err := f.assign("12", "ben", nil)
	if err != nil {
		t.Fatal(err)
	}
	doneWithPR(t, f, res.Branch, 7)
	milestoneDoc(t, f.root, "M02", "active")
	pushMilestoneBranch(t, f, "M02")
	f.be.items["12"].Fields.Milestone = "M02"
	if _, err := f.env.Reconcile(bg, f.root, true); err != nil {
		t.Fatal(err)
	}
	// The tick gates done slots with a PR only (roundtick/tick.go).
	if st := worker.LoadRegistryTolerant(f.root).Slot("ben").State(); worker.IsDone(st) {
		t.Errorf("slot is %q: the tick would gate it before the worker merges the new base", st)
	}
}

func TestReconcileSkipsARetargetWhoseBranchIsMissing(t *testing.T) {
	f, _ := milestoneFixture(t)
	res, err := f.assign("12", "ben", nil)
	if err != nil {
		t.Fatal(err)
	}
	doneWithPR(t, f, res.Branch, 7)
	milestoneDoc(t, f.root, "M02", "active") // no milestone/M02 on origin
	f.be.items["12"].Fields.Milestone = "M02"
	out, err := f.env.Reconcile(bg, f.root, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.be.retargeted) != 0 {
		t.Errorf("must not retarget onto main: %v", f.be.retargeted)
	}
	if !strings.Contains(strings.Join(out.Report.Warnings, "\n"), "milestone/M02") {
		t.Errorf("want a warning naming the missing branch: %v", out.Report.Warnings)
	}
}
