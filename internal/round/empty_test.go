package round

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/milestone"
	"github.com/l4ci/rota/internal/roundcfg"
)

func TestWhyEmptyConceptHint(t *testing.T) {
	for _, tt := range []struct {
		name, path, content, evidence string
		milestone, tracker, open      bool
	}{
		{name: "README concept", path: "README.md", content: strings.Repeat("idea\n", 10), evidence: "README.md"},
		{name: "plain README any case", path: "readme", content: strings.Repeat("idea\n", 11), evidence: "readme"},
		{name: "rst README any case", path: "ReadMe.RST", content: strings.Repeat("idea\n", 10), evidence: "ReadMe.RST"},
		{name: "docs concept", path: "docs/idea.md", evidence: "docs/idea.md"},
		{name: "nested docs concept", path: "docs/product/idea.md", evidence: "docs/product/idea.md"},
		{name: "plan concept", path: ".rota/plans/idea", evidence: ".rota/plans/idea"},
		{name: "design concept", path: ".rota/designs/idea.md", evidence: ".rota/designs/idea.md"},
		{name: "spike concept", path: ".rota/spikes/nested/idea.txt", evidence: ".rota/spikes/nested/idea.txt"},
		{name: "short README", path: "README.md", content: strings.Repeat("idea\n", 9)},
		{name: "blank lines do not count", path: "README.md", content: strings.Repeat("idea\n \t\n", 9)},
		{name: "docs other extension", path: "docs/idea.txt"},
		{name: "other rota file", path: ".rota/KNOWLEDGE.md"},
		{name: "existing milestone", path: "README.md", content: strings.Repeat("idea\n", 10), milestone: true},
		{name: "existing tracker milestone", path: "README.md", content: strings.Repeat("idea\n", 10), milestone: true, tracker: true},
		{name: "non-empty backlog", path: "README.md", content: strings.Repeat("idea\n", 10), open: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := newRepo(t, nil)
			path := filepath.Join(root, tt.path)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tt.content), 0o644); err != nil {
				t.Fatal(err)
			}
			milestoneRoot := root
			if tt.tracker {
				milestoneRoot = t.TempDir()
			}
			if tt.milestone {
				milestoneDoc(t, milestoneRoot, "M01", "planned")
			}
			remote := &fakeRemote{}
			if tt.open {
				remote.add("1", "open item", "", false, "")
			}
			var be backlog.Backend = remote
			if tt.tracker {
				be = storeBoard{Board: remote, st: milestone.FileStore{Root: milestoneRoot}}
			}
			e := Env{Git: git.Exec, Base: "main"}
			got, err := e.WhyEmpty(bg, root, be, roundcfg.ScopeOpen, nil)
			if err != nil {
				t.Fatal(err)
			}
			want := Empty{"the backlog has no open items", "capture work with /rota-capture, then run rota round candidates"}
			if tt.evidence != "" {
				want.Next = "the backlog is empty and " + tt.evidence + " holds a concept: run /rota-vision to split it into milestones, then capture issues"
			}
			if tt.open {
				want = Empty{"all 1 open items in scope open are held by a slot, in review, taken or handed to a human", "rota round status"}
				cs, err := e.Candidates(bg, root, be, CandidateOpts{Scope: roundcfg.ScopeOpen})
				if err != nil || len(cs) != 1 || cs[0].ID != "1" {
					t.Fatalf("non-empty candidates changed: %+v, %v", cs, err)
				}
			}
			if got != want {
				t.Errorf("WhyEmpty = %+v, want %+v", got, want)
			}
		})
	}
}

func TestMilestoneScopeFallsBackWithoutUnfinishedMilestones(t *testing.T) {
	root := newRepo(t, nil)
	be := &fakeRemote{}
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
	be := &fakeRemote{}
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
	be := &fakeRemote{ready: map[string][]string{"3": {"no acceptance criteria in the issue body", "no design or plan note"}}}
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
