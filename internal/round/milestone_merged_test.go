package round

import (
	"testing"

	"github.com/l4ci/rota/internal/roundcfg"
)

// Milestone merges close their issues on the tracker. That state must flow
// through both candidate selection and assignment without re-offering the work.
func TestMilestoneMergedDependencyIsReadyAndNotOffered(t *testing.T) {
	f := newAssignFixture(t)
	f.be.details["12"] = "## Acceptance\n- [ ] works\n\n## Depends on\n- #13\n"
	f.be.items["13"].Closed = true
	f.be.items["13"].Labels = []string{"on-milestone/M01"}
	cs, err := f.env.Candidates(bg, f.root, f.be, CandidateOpts{Scope: roundcfg.ScopeMilestone})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range cs {
		if c.ID == "13" {
			t.Fatal("already merged milestone issue offered again")
		}
		if c.ID == "12" {
			found = true
			if !c.Ready() {
				t.Fatalf("same-milestone dependency blocks: %+v", c)
			}
		}
	}
	if !found {
		t.Fatal("dependent issue not offered")
	}
	if _, err := f.assign("12", "ben", nil); err != nil {
		t.Fatalf("assign dependent: %v", err)
	}
}
