package backlog

import (
	"errors"
	"testing"

	"github.com/l4ci/rota/internal/backlog/trackertest"
	ms "github.com/l4ci/rota/internal/milestone"
	"github.com/l4ci/rota/internal/tracker"
)

// Issues and Umbrella both hand out a ms.Store; the return types assert it.
var (
	_ func(*Issues) ms.Store   = (*Issues).MilestoneStore
	_ func(*Umbrella) ms.Store = (*Umbrella).MilestoneStore
)

func TestTrackerStoreRoundTrip(t *testing.T) {
	f := &trackertest.MS{Fake: &trackertest.Fake{}, Native: []tracker.Milestone{}}
	st := msIssues(f).MilestoneStore()
	if !st.OnTracker() {
		t.Fatal("tracker store reports OnTracker false")
	}
	id, err := st.Add("Title", "Summary.", []string{"M01"})
	if err != nil || id != "M01" {
		t.Fatalf("add = %q %v", id, err)
	}
	if changed, err := st.SetStatus(id, "active"); err != nil || !changed {
		t.Fatalf("set status = %v %v", changed, err)
	}
	if changed, err := st.SetStatus(id, "active"); err != nil || changed {
		t.Fatalf("repeat set status = %v %v", changed, err)
	}
	list, err := st.List()
	if err != nil || len(list) != 1 || list[0].Status != "active" || ms.ActiveIDs(list)[0] != id {
		t.Fatalf("list = %+v %v", list, err)
	}
	text, err := st.Show(id)
	if err != nil || text[len(text)-1] != '\n' {
		t.Fatalf("show = %q %v", text, err)
	}
	if _, err := st.Put(id, "no frontmatter"); !errors.Is(err, ErrMilestoneText) {
		t.Fatalf("put without id: %v", err)
	}
	if changed, err := st.Put(id, text); err != nil || changed {
		t.Fatalf("put same text = %v %v", changed, err)
	}
}
