package round

import (
	"errors"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/milestone"
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
	f.labelled("20", "on-milestone/M02")
	f.labelled("21", "on-milestone/M01")
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

func TestCandidateDoesNotFlagClosedMilestoneTagWithoutMergeLabel(t *testing.T) {
	for _, branch := range []bool{false, true} {
		t.Run(map[bool]string{false: "no milestone branch", true: "milestone branch exists"}[branch], func(t *testing.T) {
			var f *assignFixture
			if branch {
				f = crossFixture(t)
				sh(t, f.root, "push", "-q", "origin", "main:refs/heads/milestone/M02")
			} else {
				f = newAssignFixture(t)
				milestoneDoc(t, f.root, "M02", "active")
				f.be.add("20", "Already on main", "M02", true, "")
			}
			f.labelled("20")
			f.be.details["12"] = "## Acceptance\n- [ ] ok\n\n## Depends on\n- #20"
			c := candidate(t, f, "12")
			if len(c.CrossMilestone) != 0 || c.BlockedBy != "" {
				t.Fatalf("main dependency flagged: %+v", c)
			}
			if _, err := f.assign("12", "", nil); err != nil {
				t.Fatalf("main dependency refused: %v", err)
			}
		})
	}
}

func TestCrossMilestoneChecksUntaggedDependencies(t *testing.T) {
	for _, operation := range []string{"candidates", "assign"} {
		t.Run(operation, func(t *testing.T) {
			f := crossFixture(t)
			f.be.items["12"].Fields.Milestone = ""
			if _, err := f.env.Start(bg, f.root, startOpts(roundcfg.ScopeOpen, 100)); err != nil {
				t.Fatal(err)
			}
			f.be.details["12"] = "## Acceptance\n- [ ] ok\n\n## Depends on\n- #20"
			if operation == "candidates" {
				c := candidate(t, f, "12")
				flag := strings.Join(c.CrossMilestone, "\n")
				if c.BlockedBy != BlockCrossMilestone || !strings.Contains(flag, "main") || !strings.Contains(flag, "M02") {
					t.Fatalf("untagged dependency not flagged: %+v", c)
				}
			} else {
				_, err := f.assign("12", "", nil)
				if err == nil {
					t.Fatal("assign accepted dependency confined to M02")
				}
				if by := blockedBy(t, err); by != BlockCrossMilestone {
					t.Fatalf("blocked by %q: %v", by, err)
				}
			}
		})
	}
}

func TestAssignChecksCrossMilestoneBundleMembers(t *testing.T) {
	f := crossFixture(t)
	f.be.add("15", "Bundle member", "M01", false, "## Acceptance\n- [ ] ok\n\n## Depends on\n- #20")
	_, err := f.assign("12", "", bundleOf("15"))
	if err == nil {
		t.Fatal("assign accepted bundle member dependent on M02")
	}
	if by := blockedBy(t, err); by != BlockCrossMilestone || !strings.Contains(err.Error(), "15") {
		t.Fatalf("blocked by %q: %v", by, err)
	}
	if len(f.be.claims) != 0 {
		t.Fatalf("refused bundle claimed: %v", f.be.claims)
	}
	if _, err := f.assign("12", "", func(o *AssignOpts) { o.Bundle = []string{"15"}; o.AcceptCrossMilestone = true }); err != nil {
		t.Fatalf("override refused bundle: %v", err)
	}
}

// crossDetailBackend injects a tracker body-read failure at the backend boundary.
type crossDetailBackend struct {
	Board
	id  string
	err error
}

func (b crossDetailBackend) Detail(id string) (string, bool, error) {
	if id == b.id {
		return "", false, b.err
	}
	return b.Board.Detail(id)
}

func TestCrossMilestonePropagatesDetailErrors(t *testing.T) {
	for _, id := range []string{"12", "24"} {
		for _, missing := range []bool{false, true} {
			t.Run(id+map[bool]string{false: "/unavailable", true: "/missing"}[missing], func(t *testing.T) {
				f := crossFixture(t)
				f.be.details["12"] = "## Acceptance\n- [ ] ok\n\n## Touches\n- `post /shared`"
				failure := errors.New("tracker unavailable")
				if missing {
					failure = backlog.ErrNotFound
				}
				be := crossDetailBackend{Board: f.be, id: id, err: failure}
				_, err := f.env.Candidates(bg, f.root, be, CandidateOpts{Scope: roundcfg.ScopeSlate, Slate: []string{"12"}})
				if missing {
					if err != nil {
						t.Fatalf("not-found should be handled by readiness: %v", err)
					}
				} else if !errors.Is(err, failure) {
					t.Fatalf("Detail(%s) error swallowed: %v", id, err)
				}
			})
		}
	}
}

type crossReadBackend struct {
	Board
	store   milestone.Store
	lists   int
	details map[string]int
}

func (b *crossReadBackend) MilestoneStore() milestone.Store { return b.store }
func (b *crossReadBackend) List(closed bool) ([]backlog.Item, error) {
	b.lists++
	return b.Board.List(closed)
}
func (b *crossReadBackend) Detail(id string) (string, bool, error) {
	b.details[id]++
	return b.Board.Detail(id)
}

type crossReadStore struct {
	milestone.Store
	calls int
	err   error
}

func (s *crossReadStore) List() ([]milestone.Entry, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.Store.List()
}
func TestCrossMilestoneSkipsUnneededAssignReads(t *testing.T) {
	f := newAssignFixture(t)
	f.be.items["12"].Fields.Milestone = ""
	f.be.add("20", "On main", "M02", true, "")
	f.be.details["12"] = "## Acceptance\n- [ ] ok\n\n## Depends on\n- #20"
	if _, err := f.env.Start(bg, f.root, startOpts(roundcfg.ScopeOpen, 100)); err != nil {
		t.Fatal(err)
	}
	store := &crossReadStore{err: errors.New("milestones must not be read")}
	be := &crossReadBackend{Board: f.be, store: store, details: map[string]int{}}
	_, err := f.env.Assign(bg, f.root, be, AssignOpts{ID: "12", CheckOnly: true})
	if err != nil {
		t.Fatalf("unrelated milestone reads blocked assign: %v", err)
	}
	if be.lists != 1 {
		t.Fatalf("open list read %d times, want only scope preflight", be.lists)
	}
	if store.calls != 0 {
		t.Fatalf("milestone list read %d times", store.calls)
	}
}

func TestCrossMilestoneCachesSharedTouchBodies(t *testing.T) {
	f := crossFixture(t)
	for _, id := range []string{"12", "13"} {
		f.be.details[id] = "## Acceptance\n- [ ] ok\n\n## Touches\n- `post /shared`"
	}
	store := &crossReadStore{Store: milestone.FileStore{Root: f.root}}
	be := &crossReadBackend{Board: f.be, store: store, details: map[string]int{}}
	cs, err := f.env.Candidates(bg, f.root, be, CandidateOpts{Scope: roundcfg.ScopeSlate, Slate: []string{"12", "13"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 2 {
		t.Fatalf("candidates: %+v", cs)
	}
	for _, c := range cs {
		if c.BlockedBy != BlockCrossMilestone {
			t.Fatalf("shared touch missed: %+v", c)
		}
	}
	if be.details["24"] != 1 {
		t.Fatalf("peer body fetched %d times, want once per pass", be.details["24"])
	}
	if store.calls != 1 {
		t.Fatalf("milestones fetched %d times, want once per pass", store.calls)
	}
}

func TestCrossMilestoneUsesMergeLabelRatherThanDependencyTag(t *testing.T) {
	for _, tc := range []struct {
		name, tag, label, status string
		flagged                  bool
	}{
		{"label names other milestone", "M01", "on-milestone/M02", "active", true},
		{"label names own milestone", "M02", "on-milestone/M01", "active", false},
		{"label names planned milestone", "M02", "on-milestone/M03", "active", false},
		{"label outlives release", "M02", "on-milestone/M02", "shipped", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := crossFixture(t)
			milestoneDoc(t, f.root, "M02", tc.status)
			f.be.items["20"].Fields.Milestone = tc.tag
			f.labelled("20", tc.label)
			f.be.details["12"] = "## Acceptance\n- [ ] ok\n\n## Depends on\n- #20"
			c := candidate(t, f, "12")
			if got := len(c.CrossMilestone) > 0; got != tc.flagged {
				t.Fatalf("flagged=%v, want %v: %+v", got, tc.flagged, c)
			}
		})
	}
}

func TestCandidateFlagsAnOpenMilestoneDependency(t *testing.T) {
	f := crossFixture(t)
	f.be.details["12"] = "## Acceptance\n- [ ] ok\n\n## Depends on\n- #24"
	c := candidate(t, f, "12")
	flag := strings.Join(c.CrossMilestone, "\n")
	if c.BlockedBy != BlockCrossMilestone || !strings.Contains(flag, "M01") || !strings.Contains(flag, "M02 (open)") {
		t.Fatalf("open dependency not flagged: %+v", c)
	}
}

func TestCrossMilestoneSkipsUnneededCandidateMilestoneReads(t *testing.T) {
	f := newAssignFixture(t)
	f.be.items["12"].Fields.Milestone = ""
	f.be.add("20", "On main", "M02", true, "")
	f.be.details["12"] = "## Acceptance\n- [ ] ok\n\n## Depends on\n- #20"
	store := &crossReadStore{err: errors.New("milestones must not be read")}
	be := &crossReadBackend{Board: f.be, store: store, details: map[string]int{}}
	cs, err := f.env.Candidates(bg, f.root, be, CandidateOpts{Scope: roundcfg.ScopeSlate, Slate: []string{"12"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || len(cs[0].CrossMilestone) != 0 || store.calls != 0 {
		t.Fatalf("unexpected milestone check: %+v; reads %d", cs, store.calls)
	}
}
