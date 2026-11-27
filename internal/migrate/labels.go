package migrate

import (
	"context"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/tracker"
)

// Relabel is an issue's migration outcome; Changed also describes preview changes.
type Relabel struct {
	Key     tracker.IssueKey
	Changed bool
}

// LabelConflictError reports issues whose other machine pins must be removed first.
type LabelConflictError struct{ Issues []string }

func (e *LabelConflictError) Error() string {
	return "conflicting machine pins: " + strings.Join(e.Issues, "; ") + "; remove the other machine label before rerunning rota migrate labels"
}

// LabelTracker is the tracker surface used by the label migration.
type LabelTracker interface {
	tracker.LabelSource
	List(context.Context, tracker.ListFilter) ([]tracker.Issue, error)
	EnsureLabels(context.Context, []string, bool) error
	AddLabels(context.Context, tracker.IssueKey, []string, bool) error
	RemoveLabels(context.Context, tracker.IssueKey, []string) error
}

// RelabelSupport replaces the retired support label on open issues. Conflicts
// remain untouched while the rest migrate; tracker errors stop immediately.
// Adding before removing preserves the source label on a partial failure, so
// another invocation can safely finish the migration.
func RelabelSupport(ctx context.Context, t LabelTracker, from, to string, apply bool) ([]Relabel, error) {
	rows := []Relabel{}
	if from == "" || to == "" {
		return rows, fmt.Errorf("source and destination labels must be non-empty")
	}
	issues, err := t.List(ctx, tracker.ListFilter{State: "open", Labels: []string{from}})
	if err != nil {
		return rows, err
	}
	conflicts := &LabelConflictError{}
	ensured := false
	for _, is := range issues {
		// Backends and fakes may return a superset of the requested filter.
		if is.State != "open" || !tracker.HasLabel(t, is.Labels, from) {
			continue
		}
		conflict := false
		for _, pin := range roundcfg.PinsOf(is.Labels, tracker.LabelsFor(t).CutPrefix) {
			if !tracker.HasLabel(t, []string{roundcfg.MachineLabel(pin)}, to) {
				conflicts.Issues = append(conflicts.Issues, is.Key.String()+": "+roundcfg.MachineLabel(pin))
				conflict = true
			}
		}
		if conflict {
			rows = append(rows, Relabel{Key: is.Key})
			continue
		}
		if from == to {
			rows = append(rows, Relabel{Key: is.Key})
			continue
		}
		if apply {
			if !ensured {
				if err := t.EnsureLabels(ctx, []string{to}, true); err != nil {
					return rows, err
				}
				ensured = true
			}
			wrote := false
			if !tracker.HasLabel(t, is.Labels, to) {
				if err := t.AddLabels(ctx, is.Key, []string{to}, true); err != nil {
					return rows, err
				}
				wrote = true
			}
			if err := t.RemoveLabels(ctx, is.Key, []string{from}); err != nil {
				rows = append(rows, Relabel{Key: is.Key, Changed: wrote})
				return rows, err
			}
		}
		rows = append(rows, Relabel{Key: is.Key, Changed: true})
	}
	if len(conflicts.Issues) > 0 {
		return rows, conflicts
	}
	return rows, nil
}
