package trackertest

import (
	"context"
	"slices"

	"github.com/l4ci/rota/internal/tracker"
)

// MS is Fake with the native milestone calls a milestone tracker needs, and a
// List that honours the label filter like a forge. Native holds the native
// milestones; FailMS, when set, makes CreateMilestone fail.
type MS struct {
	*Fake
	Native []tracker.Milestone
	FailMS error
}

// List filters by the filter's labels, as the forges do.
func (f *MS) List(ctx context.Context, fl tracker.ListFilter) ([]tracker.Issue, error) {
	all, err := f.Fake.List(ctx, fl)
	var out []tracker.Issue
	for _, is := range all {
		ok := true
		for _, l := range fl.Labels {
			ok = ok && slices.Contains(is.Labels, l)
		}
		if ok {
			out = append(out, is)
		}
	}
	return out, err
}

// Milestones lists every native milestone whatever the state asked for.
func (f *MS) Milestones(context.Context, string) ([]tracker.Milestone, error) {
	return slices.Clone(f.Native), nil
}

// CreateMilestone adds an open native milestone.
func (f *MS) CreateMilestone(_ context.Context, title, desc string) (int, error) {
	if f.FailMS != nil {
		return 0, f.FailMS
	}
	n := len(f.Native) + 1
	f.Native = append(f.Native, tracker.Milestone{Number: n, Title: title, Description: desc, State: "open"})
	f.Fake.Milestones = append(f.Fake.Milestones, title)
	return n, nil
}

// EditMilestone applies a state change.
func (f *MS) EditMilestone(_ context.Context, n int, e tracker.MilestoneEdit) error {
	for i := range f.Native {
		if f.Native[i].Number == n && e.State != nil {
			f.Native[i].State = *e.State
		}
	}
	return nil
}
