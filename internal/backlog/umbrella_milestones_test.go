package backlog

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog/trackertest"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/repos"
	"github.com/l4ci/rota/internal/tracker"
)

// msUmbrella is an umbrella over two milestone-capable fake trackers; web is
// the home repo (first registered).
func msUmbrella(t *testing.T, web, api []tracker.Milestone) (*Umbrella, map[string]*trackertest.MS) {
	t.Helper()
	fakes := map[string]*trackertest.MS{
		"web": {Fake: &trackertest.Fake{}, Native: web},
		"api": {Fake: &trackertest.Fake{}, Native: api},
	}
	u := &Umbrella{Cfg: jsonx.NewObject(),
		Repos: []repos.Repo{{Name: "web", Rel: "web", Path: "/umb/web"}, {Name: "api", Rel: "api", Path: "/umb/api"}},
		NewTracker: func(dir string) (Tracker, error) {
			return fakes[strings.TrimPrefix(dir, "/umb/")], nil
		}}
	return u, fakes
}

func TestUmbrellaMilestoneAddMintsOverAllSubRepos(t *testing.T) {
	u, fakes := msUmbrella(t,
		[]tracker.Milestone{{Number: 1, Title: "M02 — a", State: "open"}},
		[]tracker.Milestone{{Number: 1, Title: "M04 — Legacy", State: "open"}})
	id, err := u.MilestoneAdd("", "Alpha", "First", nil, "2026-10-02")
	if err != nil || id != "M05" {
		t.Fatalf("add = %q %v", id, err)
	}
	if got := fakes["web"].Native[len(fakes["web"].Native)-1].Title; got != "M05 — Alpha" {
		t.Errorf("home native = %q", got)
	}
	if len(fakes["api"].Native) != 1 {
		t.Errorf("api milestones must be untouched: %v", fakes["api"].Native)
	}
	if len(fakes["web"].Issues) != 1 || fakes["web"].Issues[0].Title != "M05 — Alpha" {
		t.Errorf("tracking issue must be on the home repo: %+v", fakes["web"].Issues)
	}
}

func TestUmbrellaMilestoneStatusSyncsEverySubRepo(t *testing.T) {
	u, fakes := msUmbrella(t, nil, nil)
	if _, err := u.MilestoneAdd("", "Alpha", "First", nil, "2026-10-02"); err != nil {
		t.Fatal(err)
	}
	if _, err := fakes["api"].CreateMilestone(nil, "M01 — Alpha", ""); err != nil {
		t.Fatal(err)
	}
	status := func() string {
		rows, err := u.MilestoneList()
		if err != nil || len(rows) != 1 {
			t.Fatalf("list = %v %v", rows, err)
		}
		return rows[0].Status
	}
	if err := u.MilestoneStatus("M01", "shipped"); err != nil {
		t.Fatal(err)
	}
	for name, f := range fakes {
		if f.Native[len(f.Native)-1].State != "closed" {
			t.Errorf("%s native not closed: %+v", name, f.Native)
		}
	}
	if s := status(); s != "shipped" {
		t.Errorf("all closed = %q, want shipped", s)
	}
	// One sub-repo reopens its native milestone: shipped drops to active.
	open := "open"
	if err := fakes["api"].EditMilestone(nil, 1, tracker.MilestoneEdit{State: &open}); err != nil {
		t.Fatal(err)
	}
	if s := status(); s != "active" {
		t.Errorf("one open = %q, want active", s)
	}
	if err := u.MilestoneStatus("M01", "planned"); err != nil {
		t.Fatal(err)
	}
	for name, f := range fakes {
		if f.Native[len(f.Native)-1].State != "open" {
			t.Errorf("%s native not reopened: %+v", name, f.Native)
		}
	}
}

func TestUmbrellaMilestoneListReadyFollowsAggregatedStatus(t *testing.T) {
	u, fakes := msUmbrella(t, nil, nil)
	if _, err := u.MilestoneAdd("", "One", "s", nil, "2026-10-02"); err != nil {
		t.Fatal(err)
	}
	if _, err := u.MilestoneAdd("", "Two", "s", []string{"M01"}, "2026-10-02"); err != nil {
		t.Fatal(err)
	}
	fakes["api"].CreateMilestone(nil, "M01 — One", "")
	if err := u.MilestoneStatus("M01", "shipped"); err != nil {
		t.Fatal(err)
	}
	open := "open"
	fakes["api"].EditMilestone(nil, 1, tracker.MilestoneEdit{State: &open})
	rows, err := u.MilestoneList()
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Status != "active" || rows[1].Ready {
		t.Errorf("M01 still open in api: rows = %+v", rows)
	}
}
