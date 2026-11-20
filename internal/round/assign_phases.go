package round

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/worker"
)

// The phases of planAssign. Each takes only what it reads and makes the
// refusals of its own phase, so a test drives one with plain inputs.

// noFreeSlotRefusal is the refusal when pickSlots found no slot: skipped and
// unresolved are the slots it passed over and why.
func noFreeSlotRefusal(reg worker.Registry, set roundcfg.Settings, id string, skipped, unresolved []string) *BlockedError {
	if len(unresolved) > 0 && len(skipped) == 0 {
		return blocked(blockedby.NoFreeSlot, "every free roster slot has an unresolved Core delivery: %s; to start a fresh session on one, run rota round assign %s --agent <slot>", strings.Join(unresolved, "; "), id)
	}
	if len(skipped) > 0 {
		return blocked(blockedby.NoLogin, "every free roster slot has an account with no login: %s", strings.Join(skipped, "; "))
	}
	active, _ := Members(reg, set)
	var missing []string
	for _, n := range active {
		if reg.Slot(n) == nil {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		msg := "round slot(s) not provisioned (a reaped slot is gone): " + strings.Join(missing, ", ") + "; run rota round start"
		if len(missing) < len(active) {
			msg += "; the other slots are busy"
		}
		return blocked(blockedby.NoFreeSlot, "%s", msg)
	}
	return blocked(blockedby.NoFreeSlot, "every roster slot is busy")
}

// selectSlot is the slot for id: the named one, else the first idle roster
// slot, else the first whose PR can be queued. warns are the slots it skipped.
func (e Env) selectSlot(ctx context.Context, root string, reg worker.Registry, o AssignOpts, id string) (slot *worker.Slot, warns []string, err error) {
	set := e.Round
	if o.Agent != "" {
		slot = reg.Slot(o.Agent)
		if slot == nil {
			return nil, nil, &exitcode.Error{Exit: exitcode.ExitResolution, Message: fmt.Sprintf("slot %s is not provisioned: run rota round start", o.Agent)}
		}
		if err := checkActive(reg, set, o.Agent, slot.Holds(id), false); err != nil {
			return nil, nil, err
		}
		if why := e.SlotLoginProblem(slot); why != "" && !(e.placesAccounts(root) && !slot.Holds(id)) {
			return nil, nil, blocked(blockedby.NoLogin, "slot %s's account has no login (%s in %s): %s", o.Agent, why, slot.ConfigDir(), loginHint(slot))
		}
		return slot, nil, nil
	}
	picked, skipped, unresolved := e.pickSlots(ctx, root, reg, set, worker.NormID(id), 1)
	if len(skipped) > 0 {
		warns = append(warns, "skipped slots whose account has no login: "+strings.Join(skipped, "; "))
	}
	if len(unresolved) > 0 {
		warns = append(warns, "skipped slots with an unresolved delivery: "+strings.Join(unresolved, "; "))
	}
	if len(picked) == 0 {
		return nil, warns, noFreeSlotRefusal(reg, set, id, skipped, unresolved)
	}
	slot = picked[0]
	// A holder resumes without the usable() gate in pickSlots: refuse it
	// like the explicit --agent path rather than re-dispatch into a login prompt.
	if slot.Holds(id) {
		if why := e.SlotLoginProblem(slot); why != "" {
			return nil, warns, blocked(blockedby.NoLogin, "slot %s's account has no login (%s in %s): %s", slot.Name(), why, slot.ConfigDir(), loginHint(slot))
		}
	}
	return slot, warns, nil
}

// slotDisposition says how the slot takes id: resuming when it already holds
// it, queue when it holds another issue but is done with a PR (it is parked
// right before the claim, so a refusal on the way moves nothing).
func (e Env) slotDisposition(ctx context.Context, root string, be Board, o AssignOpts, slot *worker.Slot, id string) (resuming, queue bool, err error) {
	agent := slot.Name()
	h := worker.HeldID(slot.Task(), slot.Branch(), agent)
	if h == "" {
		return false, false, nil
	}
	if h == worker.NormID(id) {
		return true, false, nil
	}
	e.adoptBranchPR(ctx, root, slot, !o.CheckOnly)
	if ok, why := e.parkable(ctx, root, be, slot); !ok {
		return false, false, blocked(blockedby.SlotBusy, "%s", busyMsg(agent, h, why))
	}
	return false, true, nil
}

// openPRRefusal is the refusal for an issue (or bundle member) an open PR
// already heads, nil when none does. member names a bundle member.
func openPRRefusal(id string, n int, member bool) *BlockedError {
	if member {
		return blocked(blockedby.OpenPR, "bundle member %s has open PR #%d: not ready; --accept-open-pr assigns it again for a deliberate redo", id, n)
	}
	return blocked(blockedby.OpenPR, "%s has open PR #%d: not ready; --accept-open-pr assigns it again for a deliberate redo", id, n)
}

// checkOpenPRs refuses an issue or bundle member an open PR heads. hasOpenPR
// reports a redo of the lead's open PR, which awaits the assigned worker's own done.
func (e Env) checkOpenPRs(ctx context.Context, be Board, o AssignOpts, agent string, it *backlog.Item, members []*backlog.Item, bo *bestOfRun) (hasOpenPR bool, err error) {
	// An attempt of a best-of:2 issue ignores the PR of its sibling's branch.
	openPR, err := e.openPRIssuesExcept(ctx, be, bo.otherBranch())
	if err != nil && !o.AcceptOpenPR {
		return false, err
	}
	hasOpenPR = err == nil && openPR[it.IssueKey()].Number != 0
	if o.AcceptOpenPR {
		for _, item := range append([]*backlog.Item{it}, members...) {
			pr := openPR[item.IssueKey()]
			if worker.RoundBranch(pr.Branch) && !strings.HasPrefix(pr.Branch, agent+"/") {
				return false, blocked(blockedby.OpenPR, "%s has open PR #%d on %s, owned by another agent: rota round transfer %s --to %s picks it up", item.ID, pr.Number, pr.Branch, item.ID, agent)
			}
		}
		return hasOpenPR, nil
	}
	if n := openPR[it.IssueKey()].Number; n != 0 {
		return false, openPRRefusal(it.ID, n, false)
	}
	for _, m := range members {
		if n := openPR[m.IssueKey()].Number; n != 0 {
			return false, openPRRefusal(m.ID, n, true)
		}
	}
	return hasOpenPR, nil
}

// checkCrossMilestone refuses ties across milestones: they need a maintainer
// decision even when a closed dependency would otherwise pass readiness.
func checkCrossMilestone(cross crossMilestones, items []*backlog.Item) error {
	for _, item := range items {
		flags, err := cross.check(*item)
		if err != nil {
			return err
		}
		if len(flags) > 0 {
			return blocked(blockedby.CrossMilestone, "%s: %s; --accept-cross-milestone assigns it anyway", item.ID, strings.Join(flags, "; "))
		}
	}
	return nil
}

// waiveTemplate passes the template check: its requirements gate new work,
// not work already held or deliberately restarted from an existing PR.
func waiveTemplate(r *Readiness) {
	for i := range r.Checks {
		if r.Checks[i].Name == CheckTemplate {
			r.Checks[i].OK, r.Checks[i].Detail = true, []string{}
		}
	}
}

// readinessRefusal is the refusal for a lead or bundle member that is not
// ready, nil when all are. The lead is checked first.
func readinessRefusal(lead Readiness, members []Readiness) *BlockedError {
	if !lead.Ready() {
		blk := blocked(notReadyBy(lead), "%s is not ready: %s", lead.ID, failedChecks(lead))
		blk.Readiness = &lead
		return blk
	}
	for i, mr := range members {
		if !mr.Ready() {
			blk := blocked(notReadyBy(mr), "bundle member %s is not ready: %s", mr.ID, failedChecks(mr))
			blk.Readiness = &members[i]
			return blk
		}
	}
	return nil
}

// assessReadiness assesses the lead and each bundle member against the board
// and the work in flight. The bundle never overlaps itself: what its own slot
// holds of it (on a resume) is not another slot's work.
func (e Env) assessReadiness(ctx context.Context, root string, be Board, o AssignOpts, it *backlog.Item, members []*backlog.Item, resuming, hasOpenPR bool) (lead Readiness, bundle []Readiness, err error) {
	set := e.Round
	tracked := e.trackedFiles(ctx, root)
	inFlight := e.InFlightItems(ctx, root, be, tracked, set.SharedPaths)
	own := map[string]bool{worker.NormID(it.ID): true}
	for _, m := range members {
		own[worker.NormID(m.ID)] = true
	}
	inFlight = slices.DeleteFunc(inFlight, func(x InFlight) bool { return own[worker.NormID(x.Issue)] })
	assess := func(id string) (Readiness, error) {
		r, err := AssessScoped(be, id, tracked, set.SharedPaths, inFlight, o.AcceptOverlap, o.SettledAnswers, set.BlockScopeOverlap)
		if resuming || (o.AcceptOpenPR && hasOpenPR) {
			waiveTemplate(&r)
		}
		return r, err
	}
	if lead, err = assess(it.ID); err != nil {
		return lead, nil, err
	}
	for _, m := range members {
		mr, err := assess(m.ID)
		if err != nil {
			return lead, nil, err
		}
		bundle = append(bundle, mr)
	}
	return lead, bundle, nil
}

// startPoint is the branch the issue targets (base) and the start point the
// worker's branch is cut from (from; "" for the slot's base). A support branch
// starts from the main the main machine last pushed.
func (e Env) startPoint(ctx context.Context, root string, be Board, it *backlog.Item, slot *worker.Slot, branch string, resuming bool) (from, base string, warns []string, err error) {
	// An interaction item is fixed on its integration branch, ahead of any milestone.
	base = integrationBase(root, it)
	if base == "" {
		var warn string
		if base, warn, err = e.milestoneBase(ctx, root, be, it); err != nil {
			return "", "", nil, err
		}
		if warn != "" {
			warns = append(warns, warn)
		}
	}
	switch {
	case e.Round.Role == roundcfg.RoleSupport:
		from, err = e.freshBase(ctx, root, slot, branch, resuming, strutil.FirstNonEmpty(base, slot.Base()))
	case base != "":
		// An issue of an active milestone is cut from the milestone branch.
		from, err = e.fetchBase(ctx, root, base)
	}
	return from, base, warns, err
}

// placeSlot checks the lease and the item's site, then picks the slot and how
// it takes the issue (see slotDisposition). It fills res.Agent and warnings.
func (e Env) placeSlot(ctx context.Context, root string, be Board, o AssignOpts, it *backlog.Item, res *Assigned) (reg worker.Registry, slot *worker.Slot, resuming, queue bool, err error) {
	set := e.Round
	id := it.ID
	// 1. This process holds the round's lease.
	if err := e.requireLease(ctx, root, o); err != nil {
		return reg, slot, false, false, err
	}
	if why := e.offSite(set.Support, set.Role, set.LegacySupportLabel, *it); why != "" {
		return reg, slot, false, false, blocked(blockedby.Site, "%s", why)
	}

	// 2. The slot and how it takes the issue.
	reg, err = worker.LoadRegistry(root)
	if err != nil {
		return reg, slot, false, false, err
	}
	var warns []string
	slot, warns, err = e.selectSlot(ctx, root, reg, o, id)
	res.Warnings = append(res.Warnings, warns...)
	if err != nil {
		return reg, slot, false, false, err
	}
	agent := slot.Name()
	res.Agent = agent
	// Only a gate or train on this slot's branch blocks it; other slots stay
	// assignable while it verifies in its scratch tree (#2639). A file-backend
	// claim writes tracked .rota/ files on the base checkout the gate lands
	// on, so without a forge every slot still counts as touched.
	var touched []string
	if be.Capabilities().Forge {
		touched = []string{agent}
	}
	if err := e.refuseWhileGateRuns(ctx, root, "assign", touched...); err != nil {
		return reg, slot, false, false, err
	}
	resuming, queue, err = e.slotDisposition(ctx, root, be, o, slot, id)
	if err != nil {
		return reg, slot, false, false, err
	}

	return reg, slot, resuming, queue, nil
}

// checkClaims refuses an issue another claim already covers: a PR in review,
// a stale claim to reclaim, an open PR, or a tie across milestones. hasOpenPR
// is as in checkOpenPRs.
func (e Env) checkClaims(ctx context.Context, root string, be Board, o AssignOpts, agent string, it *backlog.Item, members []*backlog.Item, reg worker.Registry, bo *bestOfRun, resuming bool) (hasOpenPR bool, err error) {
	if q := inReview(reg, it.ID, bo); q != nil && !resuming {
		return false, blocked(blockedby.Claimed, "%s has PR %s in review (from %s): rota round transfer %s --to <slot> picks it up", it.ID, q.PR, q.From, it.ID)
	}
	if o.ReclaimStale != "" {
		if err := e.checkStaleClaim(ctx, root, be, it.IssueKey(), o.ReclaimStale); err != nil {
			return false, err
		}
	}
	if !resuming {
		if hasOpenPR, err = e.checkOpenPRs(ctx, be, o, agent, it, members, bo); err != nil {
			return false, err
		}
	}
	if !o.AcceptCrossMilestone && !resuming {
		if err := checkCrossMilestone(crossMilestones{root: root, be: be}, append([]*backlog.Item{it}, members...)); err != nil {
			return false, err
		}
	}
	return hasOpenPR, nil
}
