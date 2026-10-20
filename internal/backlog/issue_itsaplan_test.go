package backlog

import (
	"fmt"
	"testing"

	"github.com/l4ci/rota/internal/connections"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/tracker/itsaplantest"
)

// On It's a Plan, `item show` reads one issue by its key and `backlog list`
// reads every open issue, past the 500 a list route would cut off.
func TestIssuesOnItsaplanShowAndList(t *testing.T) {
	s := itsaplantest.New(t, itsaplantest.Options{})
	for i := 0; i < 520; i++ {
		s.Add(itsaplantest.Issue{Title: fmt.Sprintf("task %d", i), Description: "body", Labels: []string{"bug"}})
	}
	it := tracker.NewItsaplan(connections.Connection{Name: "plan", Kind: connections.KindItsaplan, Site: s.URL}, s.Key(), "MKT")
	b := &Issues{Cfg: mustDecode(t, `{}`), Tracker: it, CountProof: stubCountProof}

	item, err := b.Get("MKT-7")
	if err != nil || item.Title != "task 6" {
		t.Fatalf("show: %+v %v", item, err)
	}
	items, err := b.List(false)
	if err != nil || len(items) != 520 {
		t.Fatalf("list: %d items, %v", len(items), err)
	}
}
