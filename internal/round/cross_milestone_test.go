package round

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/roundcfg"
)

func crossFixture(t *testing.T) *assignFixture {
	t.Helper()
	f, _ := milestoneFixture(t)
	milestoneDoc(t, f.root, "M02", "active")
	milestoneDoc(t, f.root, "M03", "planned")
	f.be.add("20", "Merged on the other milestone branch", "M02", true, "## Acceptance\n- [ ] ok")
	f.be.add("21", "Merged on its own milestone branch", "M01", true, "## Acceptance\n- [ ] ok")
	f.be.add("22", "Merged with no milestone", "", true, "## Acceptance\n- [ ] ok")
	f.be.add("23", "Closed in a planned milestone", "M03", true, "## Acceptance\n- [ ] ok")
	f.be.add("24", "Open in the other milestone", "M02", false, "## Acceptance\n- [ ] ok\n\n## Touches\n- `post /shared`")
	return f
}

func candidate(t *testing.T, f *assignFixture, id string) Candidate {
	t.Helper()
	cs, err := f.env.Candidates(bg, f.root, f.be, CandidateOpts{Scope: roundcfg.ScopeOpen})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cs {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("candidate %s not listed", id)
	return Candidate{}
}

func TestCandidateFlagsADependencyOnAnotherActiveMilestone(t *testing.T) {
	f := crossFixture(t)
	f.be.details["12"] = "## Acceptance\n- [ ] ok\n\n## Depends on\n- #20"
	c := candidate(t, f, "12")
	if c.BlockedBy != BlockCrossMilestone {
		t.Fatalf("BlockedBy = %q, want %q", c.BlockedBy, BlockCrossMilestone)
	}
	flag := strings.Join(c.CrossMilestone, "\n")
	if !strings.Contains(flag, "M01") || !strings.Contains(flag, "M02") || !strings.Contains(flag, "20 of") {
		t.Errorf("flag must name both milestones and the dependency: %q", flag)
	}
}

func TestCandidateFlagsASharedTouchWithAnotherActiveMilestone(t *testing.T) {
	f := crossFixture(t)
	f.be.details["12"] = "## Acceptance\n- [ ] ok\n\n## Touches\n- `post /shared`"
	c := candidate(t, f, "12")
	flag := strings.Join(c.CrossMilestone, "\n")
	if c.BlockedBy != BlockCrossMilestone || !strings.Contains(flag, "M02") || !strings.Contains(flag, "24 of") {
		t.Errorf("BlockedBy = %q, flag = %q", c.BlockedBy, flag)
	}
}

func TestCandidateDoesNotFlagOwnMilestoneBaseOrPlannedDependencies(t *testing.T) {
	f := crossFixture(t)
	f.be.details["12"] = "## Acceptance\n- [ ] ok\n\n## Depends on\n- #21\n- #22\n- #23"
	c := candidate(t, f, "12")
	if c.BlockedBy != "" || len(c.CrossMilestone) != 0 {
		t.Errorf("flagged: BlockedBy = %q, flag = %q", c.BlockedBy, c.CrossMilestone)
	}
}

func TestAssignRefusesACrossMilestoneDependencyUnlessAccepted(t *testing.T) {
	f := crossFixture(t)
	f.be.details["12"] = "## Acceptance\n- [ ] ok\n\n## Depends on\n- #20"
	_, err := f.assign("12", "", nil)
	if err == nil {
		t.Fatal("assign must refuse")
	}
	if by := blockedBy(t, err); by != BlockCrossMilestone || !strings.Contains(err.Error(), "M02") {
		t.Errorf("blockedBy = %q, err = %v", by, err)
	}
	if _, err := f.assign("12", "", func(o *AssignOpts) { o.AcceptCrossMilestone = true }); err != nil {
		t.Errorf("accepted assign: %v", err)
	}
}
