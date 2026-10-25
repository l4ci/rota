package backlog

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog/trackertest"
	ms "github.com/l4ci/rota/internal/milestone"
	"github.com/l4ci/rota/internal/tracker"
)

// storeBE is a Backend whose milestone store is st; only that is exercised.
type storeBE struct {
	Backend
	st ms.Store
}

func (b storeBE) MilestoneStore() ms.Store { return b.st }

func decisionItem(milestone string) *Item {
	it := &Item{ID: "12"}
	it.Fields.Milestone = milestone
	return it
}

func TestCompleteAppendsDecision(t *testing.T) {
	stores := map[string]func(t *testing.T) (ms.Store, string){
		"file": func(t *testing.T) (ms.Store, string) {
			st := ms.FileStore{Root: t.TempDir()}
			id, err := st.Add("Fog", "Find out.", nil, "delivery")
			if err != nil {
				t.Fatal(err)
			}
			return st, id
		},
		"issues": func(t *testing.T) (ms.Store, string) {
			f := &trackertest.MS{Fake: &trackertest.Fake{}, Native: []tracker.Milestone{}}
			st := msIssues(f).MilestoneStore()
			id, err := st.Add("Fog", "Find out.", nil, "delivery")
			if err != nil {
				t.Fatal(err)
			}
			return st, id
		},
	}
	for name, mk := range stores {
		t.Run(name, func(t *testing.T) {
			st, mid := mk(t)
			be := storeBE{st: st}
			got, err := LogDecision(be, decisionItem(mid), "#12", "use X")
			if err != nil || got != mid {
				t.Fatalf("%q %v", got, err)
			}
			text, _ := st.Show(mid)
			if !strings.Contains(text, "## Decisions\n\n- #12: use X\n") {
				t.Fatalf("decision line missing:\n%s", text)
			}
			if _, err := LogDecision(be, decisionItem(mid), "#12", "use X"); err != nil {
				t.Fatal(err)
			}
			again, _ := st.Show(mid)
			if again != text {
				t.Errorf("a repeat must not add a second line:\n%s", again)
			}
		})
	}
}

func TestLogDecisionSkipsWithoutMilestoneOrStore(t *testing.T) {
	st := ms.FileStore{Root: t.TempDir()}
	if got, err := LogDecision(storeBE{st: st}, decisionItem(""), "#12", "g"); err != nil || got != "" {
		t.Errorf("no milestone: %q %v", got, err)
	}
	if got, err := LogDecision(struct{ Backend }{}, decisionItem("M01"), "#12", "g"); err != nil || got != "" {
		t.Errorf("no store: %q %v", got, err)
	}
}
