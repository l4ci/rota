package tracker

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/l4ci/rota/internal/tracker/itsaplantest"
	"github.com/l4ci/rota/internal/tracker/lineartest"
)

func TestNoMilestonesAdapters(t *testing.T) {
	ctx := context.Background()
	for _, adapter := range []struct {
		name    string
		message string
		new     func(*testing.T) (IssueTracker, func() []string)
	}{
		{"linear", "issues.source linear has no milestones: Linear projects and cycles are not rota milestones", func(t *testing.T) (IssueTracker, func() []string) {
			l, s := fakeLinear(t, lineartest.Options{})
			return l, s.Requests
		}},
		{"itsaplan", "issues.source itsaplan has no milestones", func(t *testing.T) (IssueTracker, func() []string) {
			it, s := fakeItsaplan(t, itsaplantest.Options{})
			return it, s.Requests
		}},
	} {
		t.Run(adapter.name, func(t *testing.T) {
			a, requests := adapter.new(t)
			for _, call := range []struct {
				name string
				run  func() error
			}{
				{"Create", func() error { _, err := a.Create(ctx, "title", "", nil, "M01"); return err }},
				{"Edit", func() error { return a.Edit(ctx, "key", IssueEdit{Milestone: "M01"}) }},
				{"List", func() error { _, err := a.List(ctx, ListFilter{Milestone: "M01"}); return err }},
				{"Milestones", func() error { _, err := a.Milestones(ctx, "open"); return err }},
				{"CreateMilestone", func() error { _, err := a.CreateMilestone(ctx, "M01", ""); return err }},
				{"EditMilestone", func() error { return a.EditMilestone(ctx, 1, MilestoneEdit{}) }},
				{"IssuesInMilestone", func() error { _, err := a.IssuesInMilestone(ctx, "M01", "open"); return err }},
			} {
				t.Run(call.name, func(t *testing.T) {
					err := call.run()
					if !IsNoMilestones(err) {
						t.Errorf("IsNoMilestones(%v) = false, want true", err)
					}
					if !IsNoMilestones(fmt.Errorf("migrate: %w", err)) {
						t.Error("wrapped refusal was not recognised")
					}
					var te *Error
					if !errors.As(err, &te) || te.Kind != KindFailed || te.Code != 1 || te.Message != adapter.message || err.Error() != adapter.message {
						t.Errorf("refusal = %v, want KindFailed/1 with message %q", err, adapter.message)
					}
				})
			}
			if title, ok, err := a.FindMilestone(ctx, "M01"); title != "" || ok || err != nil {
				t.Errorf("FindMilestone = %q, %v, %v; want none", title, ok, err)
			}
			if got := requests(); len(got) != 0 {
				t.Errorf("milestone calls sent requests: %v", got)
			}
		})
	}
	for _, err := range []error{nil, errors.New("issues.source linear has no milestones"), failed("no milestones"), rateLimited("try later")} {
		if IsNoMilestones(err) {
			t.Errorf("IsNoMilestones(%v) = true, want false", err)
		}
	}
}
