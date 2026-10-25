package round

import (
	"slices"
	"testing"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/tracker"
)

func TestReconcileDropsClaimLabelFromClosed(t *testing.T) {
	root := newRepo(t, nil)
	f := &fakeRemote{labels: map[int][]string{
		1: {"in-progress", "claimed:dana@main", "keep"},
		2: {"claimed:ben@mac", "keep"},
		3: {"claimed:kit@main", "in-progress"},
	}, closedIssues: []tracker.Issue{
		{Key: tracker.NumKey(1), State: "closed", Labels: []string{"in-progress", "claimed:dana@main", "keep"}},
		{Key: tracker.NumKey(2), State: "closed", Labels: []string{"claimed:ben@mac", "keep"}},
	}, closed: map[int]bool{1: true, 2: true}}
	f.add("1", "Closed", "", true, "")
	f.add("2", "Closed", "", true, "")
	f.add("3", "Open", "", false, "")
	for n, ls := range f.labels {
		f.items[tracker.NumKey(n).String()].Labels = slices.Clone(ls)
	}
	e := env([]host.Agent{}, f.asForge())
	e.Label = "in-progress"
	if _, err := e.Reconcile(bg, root, false); err != nil || len(f.removed) != 0 {
		t.Fatalf("read only: removed=%v err=%v", f.removed, err)
	}
	if _, err := e.Reconcile(bg, root, true); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"1", "2"} {
		if got := f.items[id].Labels; !slices.Equal(got, []string{"keep"}) {
			t.Errorf("closed %s labels: %v want [keep]", id, got)
		}
	}
	if got := f.items["3"].Labels; !slices.Equal(got, []string{"claimed:kit@main", "in-progress"}) {
		t.Errorf("open issue labels changed: %v", got)
	}
}
