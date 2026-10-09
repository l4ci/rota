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
// records an unrecorded PR, adds a missing in-progress label, removes it from a closed issue and clears a
// claimId whose claim is gone from the tracker (never the tracker's side) and
// drops a queued PR record whose PR is merged or closed, parks a slot whose PR is merged and releases an adopted slot whose PR is merged (worktree and branch kept). Every other
// kind is only reported (a tab may be a live worker; opening a PR is the
// worker's act; resetting a slot and removing a label are the orchestrator's).
// A repair that fails becomes a warning and leaves its finding in Drift.
func (e Env) Reconcile(ctx context.Context, root string, apply bool) (Outcome, error) {
	rep, err := e.Status(ctx, root)
	if err != nil {
		return Outcome{}, err
	}
	e.findUnregisteredBranches(ctx, root, rep, worker.LoadRegistry(root)) // a branch scan: reconcile and the tick, not every status
	out := Outcome{Report: rep}
	for _, f := range rep.Findings {
		if !apply || f.Repair == "" {
			out.Drift = append(out.Drift, f)
			continue
		}
		if err := e.repair(ctx, root, rep, f); err != nil {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("repair %s %s: %v", f.Kind, firstNonEmpty(f.Slot, f.branch), err))
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
	case UnregisteredBranch:
		return e.adoptBranch(ctx, root, f)
	case MergedExternal:
		return e.workerEnv().ReleaseExternal(root, f.Slot, false)
	case PRStale:
		if f.Slot != "" {
			return e.parkMerged(ctx, root, f.Slot)
		}
		return worker.Update(root, func(doc *worker.Doc) {
			doc.DropQueued(func(q worker.QueuedPR) bool { return q.Issue == f.Issue && (f.pr == "" || q.PR == f.pr) })
		})
	case ItemTimeout:
		if e.Board == nil {
			return fmt.Errorf("no backlog to park %s on", f.Issue)
		}
		note := fmt.Sprintf("%s has run past work.itemTimeoutMinutes (%d min): %s. Parked for a human; the PR, if any, stays open.", f.Issue, e.ItemTimeoutMinutes, f.Detail)
		_, err := e.Transfer(ctx, root, e.Board, TransferOpts{Issue: f.Issue, To: HumanTarget, Note: note, HolderPID: e.HolderPID})
		return err
	case ClaimMismatch:
		return editSlot(root, f.Slot, func(s *worker.Slot) error { s.SetClaimID(""); return nil })
	case LabelMissing:
		n, err := strconv.Atoi(f.Issue)
		if err != nil {
			return err
		}
		return e.Forge.AddLabels(ctx, n, []string{e.Label}, false)
	case LabelStale:
		n, err := strconv.Atoi(f.Issue)
		if err != nil {
			return err
		}
		return e.Forge.RemoveLabels(ctx, n, []string{e.Label})
	}
	return fmt.Errorf("%s has no safe repair", f.Kind)
}

// parkMerged frees a slot that still holds a branch whose PR is merged, as
// `assign` would: the worktree moves to park/<agent> at the base and the
// registry drops the issue, claim and PR. Nothing is pushed (the work is in the
// base) and nothing is salvaged: a worker still running, or a dirty worktree,
// is left alone.
func (e Env) parkMerged(ctx context.Context, root, name string) error {
	s := worker.LoadRegistry(root).Slot(name)
	if s == nil {
		return fmt.Errorf("slot %s is not in the registry", name)
	}
	if st := s.State(); st != "done" && st != "idle" {
		return fmt.Errorf("slot %s is %s, not done or idle", name, firstNonEmpty(st, "busy"))
	}
	dirty, err := e.dirtyPaths(ctx, s.Worktree())
	if err != nil {
		return err
	}
	if len(dirty) > 0 {
		return fmt.Errorf("slot %s has uncommitted changes", name)
	}
	base := firstNonEmpty(s.Base(), e.Base)
	if _, errOut, code := e.gitOut(ctx, s.Worktree(), "switch", "-q", "-C", worker.ParkBranch(name), base); code != 0 {
		return fmt.Errorf("could not switch %s to park/%s: %s", name, name, errOut)
	}
	held := s.HeldID()
	if err := freeSlot(root, name, false); err != nil {
		return err
	}
	return worker.ClearItemStart(root, held)
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
