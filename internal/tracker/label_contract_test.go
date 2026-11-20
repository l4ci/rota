package tracker

import (
	"context"
	"slices"
	"testing"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/connections"
	"github.com/l4ci/rota/internal/tracker/itsaplantest"
	"github.com/l4ci/rota/internal/tracker/jiratest"
	"github.com/l4ci/rota/internal/tracker/lineartest"
)

// labelFixture is an adapter over its fake and whether its labels are free
// text, so an unknown label never fails.
type labelFixture struct {
	tracker  IssueTracker
	key      IssueKey
	freeText bool
}

func labelFixtures(t *testing.T) map[string]func(*testing.T) labelFixture {
	ctx := context.Background()
	finish := func(t *testing.T, tr IssueTracker, free bool) labelFixture {
		key, err := tr.Create(ctx, "label me", "", nil, "")
		if err != nil {
			t.Fatal(err)
		}
		return labelFixture{tr, key, free}
	}
	fx := map[string]func(*testing.T) labelFixture{}
	fx["Linear"] = func(t *testing.T) labelFixture {
		s := lineartest.New(t, lineartest.Options{})
		return finish(t, NewLinear(connections.Connection{Name: "work", Kind: connections.KindLinear, Site: s.URL}, linearKey, "ENG"), false)
	}
	fx["Itsaplan"] = func(t *testing.T) labelFixture {
		s := itsaplantest.New(t, itsaplantest.Options{})
		return finish(t, NewItsaplan(connections.Connection{Name: "plan", Kind: connections.KindItsaplan, Site: s.URL}, s.Key(), "MKT"), false)
	}
	fx["Jira"] = func(t *testing.T) labelFixture {
		s := jiratest.New(t, jiratest.Options{Version: 3})
		c := connections.Connection{Name: "work", Kind: connections.KindJira, Site: s.URL, User: "me@example.com", Deployment: connections.DeploymentCloud}
		j, err := NewJira(c, "tok", s.Project(), config.Label(nil, "notPlanned"))
		if err != nil {
			t.Fatal(err)
		}
		return finish(t, j, true)
	}
	return fx
}

// The label policy (IssueTracker, AddLabels and IssueEdit.NoCreateLabels): with
// auto-create off, a label the tracker does not know fails with KindNotFound
// and is neither created nor added, through AddLabels and through Edit alike.
// A free-text tracker has no unknown label and adds it. Run against every
// adapter fake.
func TestLabelContractAutoCreateOff(t *testing.T) {
	ctx := context.Background()
	for name, mk := range labelFixtures(t) {
		for _, via := range []string{"AddLabels", "Edit"} {
			t.Run(name+"/"+via, func(t *testing.T) {
				f := mk(t)
				var err error
				if via == "AddLabels" {
					err = f.tracker.AddLabels(ctx, f.key, []string{"brand-new"}, false)
				} else {
					err = f.tracker.Edit(ctx, f.key, IssueEdit{AddLabels: []string{"brand-new"}, NoCreateLabels: true})
				}
				is, gerr := f.tracker.Get(ctx, f.key, false)
				if gerr != nil {
					t.Fatal(gerr)
				}
				has := slices.Contains(is.Labels, "brand-new")
				if f.freeText {
					if err != nil || !has {
						t.Fatalf("free-text tracker: err=%v labels=%v, want the label added", err, is.Labels)
					}
					return
				}
				if !IsKind(err, KindNotFound) || has {
					t.Fatalf("err=%v labels=%v, want KindNotFound and no label", err, is.Labels)
				}
			})
		}
	}
}

// With auto-create on (the default), both paths create the missing label.
func TestLabelContractAutoCreateOn(t *testing.T) {
	ctx := context.Background()
	for name, mk := range labelFixtures(t) {
		t.Run(name, func(t *testing.T) {
			f := mk(t)
			if err := f.tracker.Edit(ctx, f.key, IssueEdit{AddLabels: []string{"made"}}); err != nil {
				t.Fatal(err)
			}
			is, _ := f.tracker.Get(ctx, f.key, false)
			if !slices.Contains(is.Labels, "made") {
				t.Fatalf("labels=%v, want made", is.Labels)
			}
		})
	}
}
