package round

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/roundcfg"
)

func TestMilestoneScopeFallsBackWithoutUnfinishedMilestones(t *testing.T) {
	root := newRepo(t, nil)
	be := &fakeBacklog{}
	be.add("1", "open one", "", false, "")
	be.add("2", "open two", "", false, "")
	e := Env{Git: git.Exec, Base: "main"}
	for _, sc := range []string{roundcfg.ScopeMilestone, roundcfg.ScopeNext} {
		cs, err := e.Candidates(bg, root, be, CandidateOpts{Scope: sc})
		if err != nil || len(cs) != 2 {
			t.Fatalf("%s with no milestone offers every open item: %v %v", sc, ids(cs), err)
		}
		if !FellBack(root, sc) {
			t.Errorf("%s should report the fallback", sc)
		}
		if ok, _ := InScope(root, be, sc, nil, "2"); !ok {
			t.Errorf("assign must agree with candidates under %s", sc)
		}
	}
	// A shipped milestone alone is no reason to hold the round back either.
	milestoneDoc(t, root, "M01", "shipped")
	if !FellBack(root, roundcfg.ScopeMilestone) {
		t.Error("only shipped milestones: still a fallback")
	}
	// A planned one keeps the strict scope.
	milestoneDoc(t, root, "M02", "planned", "M01")
	if FellBack(root, roundcfg.ScopeMilestone) {
		t.Error("a planned milestone keeps scope milestone strict")
	}
	if FellBack(root, roundcfg.ScopeOpen) || FellBack(root, roundcfg.ScopeSlate) {
		t.Error("open and slate never fall back")
	}
}

func TestWhyEmptyNamesReasonAndNextCommand(t *testing.T) {
	root := newRepo(t, nil)
	e := Env{Git: git.Exec, Base: "main"}
	be := &fakeBacklog{}
	why := func(scope string, slate ...string) Empty {
		t.Helper()
		w, err := e.WhyEmpty(bg, root, be, scope, slate)
		if err != nil {
			t.Fatal(err)
		}
		if w.Reason == "" || w.Next == "" {
			t.Fatalf("%s: reason and next are both required: %+v", scope, w)
		}
		return w
	}
	if w := why(roundcfg.ScopeOpen); !strings.Contains(w.Reason, "no open items") || !strings.Contains(w.Next, "/rota-capture") {
		t.Errorf("empty backlog: %+v", w)
	}
	be.add("1", "tagged elsewhere", "M02", false, "")
	milestoneDoc(t, root, "M01", "active")
	if w := why(roundcfg.ScopeMilestone); !strings.Contains(w.Next, "--scope open") {
		t.Errorf("empty milestone: %+v", w)
	}
	if w := why(roundcfg.ScopeSlate, "9"); !strings.Contains(w.Reason, "slate") {
		t.Errorf("slate: %+v", w)
	}
	writeRegistry(t, root, slot(root, "ben", "ben/1-held", nil))
	if w := why(roundcfg.ScopeOpen); !strings.Contains(w.Reason, "held") || !strings.Contains(w.Next, "round status") {
		t.Errorf("everything held: %+v", w)
	}
}

func TestAssessBriefTakesCriteriaFromTheBrief(t *testing.T) {
	be := &fakeBacklog{ready: map[string][]string{"3": {"no acceptance criteria in the issue body", "no design or plan note"}}}
	be.add("3", "bare", "", false, "")
	if r, err := Assess(be, "3", nil, nil, nil, false); err != nil || r.Ready() {
		t.Fatalf("a bare item is not ready: %v %v", r, err)
	}
	if r, _ := AssessBrief(be, "3", nil, nil, nil, false, "settled: use the cache"); r.Ready() {
		t.Error("a brief without criteria changes nothing")
	}
	r, err := AssessBrief(be, "3", nil, nil, nil, false, "## Acceptance\n- [ ] start offers open items\n")
	if err != nil || !r.Ready() {
		t.Errorf("a brief with criteria makes the item ready: %v %v", r, err)
	}
}
