package cli

import (
	"context"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/doctor"
	"github.com/l4ci/rota/internal/tracker"
)

func doctorLabelInput(ctx context.Context, d *Deps, root string) *doctor.LabelState {
	issue, err := issueBackend(d, root)
	if err != nil || !issue {
		return nil
	}
	state := &doctor.LabelState{}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cfg := d.Config(root)
	state.Entries, err = config.ResolvedLabels(cfg)
	if err != nil {
		state.Problem = err.Error()
		return state
	}
	tr, err := d.NewTracker(ctx, root, cfg)
	if err != nil {
		state.Problem = err.Error()
		return state
	}
	lister, ok := tr.(tracker.LabelLister)
	if !ok {
		state.Problem = "tracker does not support listing label definitions"
		return state
	}
	_, state.Metadata = tr.(tracker.LabelManager)
	state.Existing, err = lister.ListLabels(ctx)
	if err != nil {
		state.Problem = err.Error()
		return state
	}
	open, err := tr.List(ctx, tracker.ListFilter{State: "open"})
	if err != nil {
		state.Problem = err.Error()
		return state
	}
	state.Used = map[string]bool{}
	for _, issue := range open {
		for _, label := range issue.Labels {
			state.Used[label] = true
		}
	}
	if err := ctx.Err(); err != nil {
		state.Problem = err.Error()
	}
	return state
}
