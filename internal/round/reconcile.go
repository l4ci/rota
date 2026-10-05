package round

import (
	"context"
	"fmt"
	"strconv"

	"github.com/l4ci/rota/internal/worker"
)

// Outcome is what Reconcile reports: the drift still present, the drift it
// repaired, and the assembled state it worked from.
type Outcome struct {
	Report   *Report
	Drift    []Finding
	Repaired []Finding
}

// Clean: nothing drifted, so nothing needed repair.
func (o Outcome) Clean() bool { return len(o.Drift) == 0 && len(o.Repaired) == 0 }

// Reconcile assembles the round and, with apply, makes the safe repairs: it
// clears the handle of a dead tab, registers an unregistered worktree,
// records an unrecorded PR, adds a missing in-progress label and clears a
// claimId whose claim is gone from the tracker (never the tracker's side) and
// drops a queued PR record whose PR is merged or closed. Every other
// kind is only reported (a tab may be a live worker; opening a PR is the
// worker's act; resetting a slot and removing a label are the orchestrator's).
// A repair that fails becomes a warning and leaves its finding in Drift.
func (e Env) Reconcile(ctx context.Context, root string, apply bool) (Outcome, error) {
	rep, err := e.Status(ctx, root)
	if err != nil {
		return Outcome{}, err
	}
	out := Outcome{Report: rep}
	for _, f := range rep.Findings {
		if !apply || f.Repair == "" {
			out.Drift = append(out.Drift, f)
			continue
		}
		if err := e.repair(ctx, root, rep, f); err != nil {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("repair %s %s: %v", f.Kind, f.Slot, err))
			out.Drift = append(out.Drift, f)
			continue
		}
		out.Repaired = append(out.Repaired, f)
	}
	return out, nil
}

func (e Env) repair(ctx context.Context, root string, rep *Report, f Finding) error {
	v := rep.views[f.Slot]
	switch f.Kind {
	case DeadTab:
		return editSlot(root, f.Slot, func(s *worker.Slot) error {
			s.SetHandle("")
			return s.MarkState("dead", "")
		})
	case PRUnrecorded:
		if v == nil || v.openPR == nil {
			return fmt.Errorf("no open PR to record")
		}
		return editSlot(root, f.Slot, func(s *worker.Slot) error { s.SetPR(v.openPR.URL); return nil })
	case UnregisteredWorktree:
		var row Row
		for _, r := range rep.Rows {
			if r.Name == f.Slot {
				row = r
			}
		}
		return registerSlot(root, row, v)
	case PRStale: // only a queued record carries a repair
		return worker.Update(root, func(doc *worker.Doc) {
			doc.DropQueued(func(q worker.QueuedPR) bool { return q.Issue == f.Issue })
		})
	case ClaimMismatch:
		return editSlot(root, f.Slot, func(s *worker.Slot) error { s.SetClaimID(""); return nil })
	case LabelMissing:
		n, err := strconv.Atoi(f.Issue)
		if err != nil {
			return err
		}
		return e.Forge.AddLabels(ctx, n, []string{e.Label}, false)
	}
	return fmt.Errorf("%s has no safe repair", f.Kind)
}

// editSlot edits one slot under the registry lock. A slot that is not there is
// an error, and so is whatever the edit returns.
func editSlot(root, name string, edit func(*worker.Slot) error) error {
	var editErr error
	found, err := worker.UpdateSlot(root, name, func(s *worker.Slot) { editErr = edit(s) })
	if err == nil && !found {
		err = fmt.Errorf("slot %s is not in the registry", name)
	}
	if err == nil {
		err = editErr
	}
	return err
}

// registerSlot adds the worktree's slot, parked or holding its issue. It
// leaves the registry's session and round alone.
func registerSlot(root string, row Row, v *view) error {
	return worker.Update(root, func(doc *worker.Doc) {
		if doc.Slot(row.Name) != nil {
			return
		}
		s := worker.NewSlot(row.Name, row.Branch, v.worktree, v.base, row.Tab)
		if row.Issue != "" {
			s.SetTask(row.Issue)
		}
		if v.openPR != nil {
			s.SetPR(v.openPR.URL)
		}
		doc.AppendSlot(s)
	})
}
