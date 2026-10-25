package round

import (
	"context"
	"fmt"
	"github.com/l4ci/rota/internal/strutil"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/tracker"
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
// records an unrecorded PR, adds a missing in-progress label, removes claim labels from a closed issue,
// syncs visible claim labels from comment claims and clears a
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
	// A corrupt registry is not an empty pool: refuse rather than report every
	// adoptable branch as unregistered (#712).
	reg, err := worker.LoadRegistry(root)
	if err != nil {
		return Outcome{}, err
	}
	e.findUnregisteredBranches(ctx, root, rep, reg) // a branch scan: reconcile and the tick, not every status
	e.findSupportBounced(ctx, rep, reg)
	out := Outcome{Report: rep}
	for _, f := range rep.Findings {
		if !apply || f.Repair == "" {
			out.Drift = append(out.Drift, f)
			continue
		}
		if src := unreadSource(rep, f.Kind); src != "" {
			// A finding computed without its source is a guess, and repairing
			// on a guess releases or parks a slot that may be fine (#579).
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("repair %s %s skipped: %s was unreadable this run", f.Kind, strutil.FirstNonEmpty(f.Slot, f.branch), src))
			out.Drift = append(out.Drift, f)
			continue
		}
		if err := e.repair(ctx, root, rep, f); err != nil {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("repair %s %s: %v", f.Kind, strutil.FirstNonEmpty(f.Slot, f.branch), err))
			out.Drift = append(out.Drift, f)
			continue
		}
		out.Repaired = append(out.Repaired, f)
	}
	return out, nil
}

// unreadSource names the source a repair of kind depends on when the report
// could not read it: the host for a tab-derived finding, the forge for the
// PR, merge and label ones. "" when the source answered or the kind needs none.
func unreadSource(rep *Report, kind string) string {
	need := ""
	switch kind {
	case DeadTab:
		need = SourceHost
	case PRUnrecorded, MergedExternal, PRStale, LabelMissing, LabelStale, ClaimLabelDrift:
		need = SourceForge
	}
	for _, u := range rep.Unavailable {
		if u == need {
			return need
		}
	}
	return ""
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
		pr := v.recordablePR()
		if pr == nil {
			return fmt.Errorf("no PR to record")
		}
		return editSlot(root, f.Slot, func(s *worker.Slot) error { s.SetPR(pr.URL); return nil })
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
		return e.Worker.ReleaseExternal(root, f.Slot, false)
	case PRStale:
		if f.Slot != "" {
			return e.parkMerged(ctx, root, f.Slot)
		}
		return worker.Update(root, func(doc *worker.Doc) {
			doc.DropQueued(func(q worker.QueuedPR) bool {
				return worker.NormID(q.Issue) == worker.NormID(f.Issue) && (f.pr == "" || q.PR == f.pr)
			})
		})
	case ItemTimeout:
		if e.Board == nil {
			return fmt.Errorf("no backlog to park %s on", f.Issue)
		}
		note := fmt.Sprintf("%s has run past round.itemTimeoutMinutes (%d min): %s. Parked for a human; the PR, if any, stays open.", f.Issue, e.Round.ItemTimeoutMinutes, f.Detail)
		_, err := e.Transfer(ctx, root, e.Board, TransferOpts{Issue: f.Issue, To: HumanTarget, Note: note})
		return err
	case ClaimMismatch:
		return editSlot(root, f.Slot, func(s *worker.Slot) error { s.SetClaimID(""); return nil })
	case ClaimLabelDrift:
		syncer, ok := e.Board.(backlog.ClaimLabelSyncer)
		if !ok {
			return fmt.Errorf("backlog cannot sync claim labels")
		}
		_, err := syncer.SyncClaimLabels(f.Issue)
		return err
	case LabelMissing:
		n, ok := tracker.ParseKey(f.Issue)
		if !ok {
			return fmt.Errorf("%s is not an issue key", f.Issue)
		}
		return e.Forge.AddLabels(ctx, n, []string{e.Label}, false)
	case LabelStale:
		n, ok := tracker.ParseKey(f.Issue)
		if !ok {
			return fmt.Errorf("%s is not an issue key", f.Issue)
		}
		is, err := e.Forge.Get(ctx, n, false)
		if err != nil {
			return err
		}
		if is.State != "closed" {
			return nil
		}
		var drop []string
		for _, l := range is.Labels {
			if l == e.Label || strings.HasPrefix(l, backlog.ClaimLabelPrefix) {
				drop = append(drop, l)
			}
		}
		if len(drop) == 0 {
			return nil
		}
		return e.Forge.RemoveLabels(ctx, n, drop)
	}
	return fmt.Errorf("%s has no safe repair", f.Kind)
}

// parkMerged frees a slot that still holds a branch whose PR is merged, as
// `assign` would: the worktree moves to park/<agent> at the base and the
// registry drops the issue, claim and PR. Nothing is pushed (the work is in the
// base) and nothing is salvaged: a worker still running, or a dirty worktree,
// is left alone. A dead or blocked slot is parked too (a relay can leave one blocked after its PR merged), whenever it was marked: its tab is
// gone and the merged PR means nothing is lost.
func (e Env) parkMerged(ctx context.Context, root, name string) error {
	reg, err := worker.LoadRegistry(root)
	if err != nil {
		return err
	}
	s := reg.Slot(name)
	if s == nil {
		return fmt.Errorf("slot %s is not in the registry", name)
	}
	if st := s.State(); !worker.Reclaimable(st) {
		return fmt.Errorf("slot %s is %s, not done, idle, dead or blocked", name, strutil.FirstNonEmpty(st, "busy"))
	}
	dirty, err := e.dirtyPaths(ctx, s.Worktree())
	if err != nil {
		return err
	}
	if len(dirty) > 0 {
		return fmt.Errorf("slot %s has uncommitted changes", name)
	}
	base := strutil.FirstNonEmpty(s.Base(), e.Base)
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
