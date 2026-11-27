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
	"github.com/l4ci/rota/internal/tracker"
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
// slot, else the first whose PR can be queued; a slot a running gate holds
// comes last. warns are the slots it skipped.
func (e Env) selectSlot(ctx context.Context, root string, reg worker.Registry, forge bool, o AssignOpts, id string) (slot *worker.Slot, warns []string, err error) {
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
	// While a gate holds the land lock, an assign to a slot it gates refuses
	// (#2639), and the tick then defers every later candidate. Pick among all
	// candidates and take the first the gate does not hold (#4246); when every
	// one is gated, keep the first so the assign refuses with gate-running.
	gateBlocks, err := e.gateBlocksSlot(ctx, root, forge)
	if err != nil {
		return nil, nil, err
	}
	want := 1
	if gateBlocks != nil {
		want = len(set.Roster)
	}
	picked, skipped, unresolved := e.pickSlots(ctx, root, reg, set, worker.NormID(id), want)
	if gateBlocks != nil {
		// A holder of this issue resumes; it stays first whatever the gate says.
		i := slices.IndexFunc(picked, func(s *worker.Slot) bool { return s.Holds(id) || !gateBlocks(s.Name()) })
		if i > 0 {
			picked = picked[i:]
		}
	}
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
	held := worker.HeldID(slot.Task(), slot.Branch(), slot.Name())
	return disposition(held, id, slot.Name(), func() (bool, string) {
		e.adoptBranchPR(ctx, root, slot, !o.CheckOnly)
		return e.parkable(ctx, root, be, slot)
	})
}

// disposition is slotDisposition on plain inputs: held is the issue the slot
// holds ("" for none), parkable says whether a slot holding another issue is
// done with its PR (and why not), asked only then.
func disposition(held, id, agent string, parkable func() (bool, string)) (resuming, queue bool, err error) {
	if held == "" {
		return false, false, nil
	}
	if held == worker.NormID(id) {
		return true, false, nil
	}
	if ok, why := parkable(); !ok {
		return false, false, blocked(blockedby.SlotBusy, "%s", busyMsg(agent, held, why))
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

// openPRClaims refuses an issue or bundle member an open PR heads. openPR is
// the open PRs by issue and listErr why they could not be listed. hasOpenPR
// reports a redo of the lead's open PR, which awaits the assigned worker's own done.
func openPRClaims(openPR map[tracker.IssueKey]tracker.PR, listErr error, acceptOpenPR bool, agent string, it *backlog.Item, members []*backlog.Item) (hasOpenPR bool, err error) {
	if listErr != nil && !acceptOpenPR {
		return false, listErr
	}
	hasOpenPR = listErr == nil && openPR[it.IssueKey()].Number != 0
	if acceptOpenPR {
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
func checkCrossMilestone(check func(backlog.Item) ([]string, error), items []*backlog.Item) error {
	for _, item := range items {
		flags, err := check(*item)
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

// readinessIn is what assessReadiness reads: the facts planAssign gathered
// once, with assess standing for AssessScoped against the board.
type readinessIn struct {
	lead     *backlog.Item
	members  []*backlog.Item
	inFlight []InFlight // the work in flight on every slot, the bundle's own included
	// waiveTemplate passes the template check: a resume or a deliberate redo.
	waiveTemplate bool
	assess        func(id string, inFlight []InFlight) (Readiness, error)
}

// assessReadiness assesses the lead and each bundle member against the board
// and the work in flight. The bundle never overlaps itself: what its own slot
// holds of it (on a resume) is not another slot's work.
func assessReadiness(in readinessIn) (lead Readiness, bundle []Readiness, err error) {
	own := map[string]bool{worker.NormID(in.lead.ID): true}
	for _, m := range in.members {
		own[worker.NormID(m.ID)] = true
	}
	inFlight := slices.DeleteFunc(slices.Clone(in.inFlight), func(x InFlight) bool { return own[worker.NormID(x.Issue)] })
	assess := func(id string) (Readiness, error) {
		r, err := in.assess(id, inFlight)
		if in.waiveTemplate {
			waiveTemplate(&r)
		}
		return r, err
	}
	if lead, err = assess(in.lead.ID); err != nil {
		return lead, nil, err
	}
	for _, m := range in.members {
		mr, err := assess(m.ID)
		if err != nil {
			return lead, nil, err
		}
		bundle = append(bundle, mr)
	}
	return lead, bundle, nil
}

// readinessFor gathers the readiness facts from the board and runs assessReadiness.
func (e Env) readinessFor(ctx context.Context, root string, be Board, o AssignOpts, it *backlog.Item, members []*backlog.Item, resuming, hasOpenPR bool) (Readiness, []Readiness, error) {
	set := e.Round
	held, err := e.labelled(ctx, be, e.OnHold)
	if err != nil {
		return Readiness{}, nil, err
	}
	keys := map[string]tracker.IssueKey{it.ID: it.IssueKey()}
	for _, member := range members {
		keys[member.ID] = member.IssueKey()
	}
	tracked := e.trackedFiles(ctx, root)
	return assessReadiness(readinessIn{
		lead: it, members: members,
		inFlight:      e.InFlightItems(ctx, root, be, tracked, set.SharedPaths),
		waiveTemplate: resuming || (o.AcceptOpenPR && hasOpenPR),
		assess: func(id string, inFlight []InFlight) (Readiness, error) {
			r, err := AssessScoped(be, id, tracked, set.SharedPaths, inFlight, o.AcceptOverlap, o.SettledAnswers, set.BlockScopeOverlap)
			if err != nil {
				return r, err
			}
			if held[keys[id]] {
				r.Checks = append(r.Checks, Check{Name: e.OnHold, Detail: []string{"on hold: a person must remove " + e.OnHold + " before work starts"}})
			}
			return r, nil
		},
	})
}

// startIn is what startPoint reads. The closures are the lookups it makes
// only on the branch that needs them.
type startIn struct {
	support     bool   // round.role is support
	integration string // the item's integration branch, "" for none
	slotBase    string
	resuming    bool
	milestone   func() (base, warning string, err error)
	freshBase   func(target string) (string, error)
	fetchBase   func(base string) (string, error)
}

// startPoint is the branch the issue targets (base) and the start point the
// worker's branch is cut from (from; "" for the slot's base). A support branch
// starts from the main the main machine last pushed.
func startPoint(in startIn) (from, base string, warns []string, err error) {
	// An interaction item is fixed on its integration branch, ahead of any milestone.
	base = in.integration
	if base == "" {
		var warn string
		if base, warn, err = in.milestone(); err != nil {
			return "", "", nil, err
		}
		if warn != "" {
			warns = append(warns, warn)
		}
	}
	switch {
	case in.support:
		from, err = in.freshBase(strutil.FirstNonEmpty(base, in.slotBase))
	case base != "":
		// An issue of an active milestone is cut from the milestone branch.
		from, err = in.fetchBase(base)
	}
	return from, base, warns, err
}

// startPointFor gathers the start-point inputs and runs startPoint.
func (e Env) startPointFor(ctx context.Context, root string, be Board, it *backlog.Item, slot *worker.Slot, branch string, resuming bool) (from, base string, warns []string, err error) {
	return startPoint(startIn{
		support:     e.Round.Role == roundcfg.RoleSupport,
		integration: integrationBase(root, it),
		slotBase:    slot.Base(),
		resuming:    resuming,
		milestone:   func() (string, string, error) { return e.milestoneBase(ctx, root, be, it) },
		freshBase: func(target string) (string, error) {
			return e.freshBase(ctx, root, slot, branch, resuming, target)
		},
		fetchBase: func(base string) (string, error) { return e.fetchBase(ctx, root, base) },
	})
}

// placement is where an issue goes: the registry, the slot that takes it and how.
type placement struct {
	reg      worker.Registry
	slot     *worker.Slot
	resuming bool // the slot already holds the issue
	queue    bool // the slot holds another issue but is done with a PR
}

// gateTouched is the slots a running gate or train blocks for an assignment.
// Only a gate on this slot's branch blocks it; other slots stay assignable
// while it verifies in its scratch tree (#2639). A file-backend claim writes
// tracked .rota/ files on the base checkout the gate lands on, so without a
// forge every slot still counts as touched (nil).
func gateTouched(forge bool, agent string) []string {
	if forge {
		return []string{agent}
	}
	return nil
}

// placeSlot checks the lease and the item's site, then picks the slot and how
// it takes the issue (see slotDisposition). It fills res.Agent and warnings.
func (e Env) placeSlot(ctx context.Context, root string, be Board, o AssignOpts, it *backlog.Item, res *Assigned) (placement, error) {
	set := e.Round
	id := it.ID
	// 1. This process holds the round's lease.
	if err := e.requireLease(ctx, root, o); err != nil {
		return placement{}, err
	}
	if why := e.offSite(set.Support, set.Role, set.LegacySupportLabel, *it); why != "" {
		return placement{}, blocked(blockedby.Site, "%s", why)
	}

	// 2. The slot and how it takes the issue.
	reg, err := worker.LoadRegistry(root)
	if err != nil {
		return placement{}, err
	}
	slot, warns, err := e.selectSlot(ctx, root, reg, be.Capabilities().Forge, o, id)
	res.Warnings = append(res.Warnings, warns...)
	if err != nil {
		return placement{}, err
	}
	agent := slot.Name()
	res.Agent = agent
	forge := be.Capabilities().Forge
	touched := gateTouched(forge, agent)
	// A done slot's PR moves to the review list first, and the gate verifies in
	// its scratch tree, so a gate on that PR does not block the move (#4248).
	// Only a holder naming no slot or PR still does. The disposition runs
	// first to tell the move from a refusal.
	var (
		resuming, queue bool
		derr            error
		decided         bool
	)
	if forge && worker.HeldID(slot.Task(), slot.Branch(), agent) != "" {
		resuming, queue, derr = e.slotDisposition(ctx, root, be, o, slot, id)
		decided = true
	}
	if decided && queue {
		if err := e.refuseGate(ctx, root, "assign", true, touched...); err != nil {
			return placement{}, err
		}
	} else if err := e.refuseWhileGateRuns(ctx, root, "assign", touched...); err != nil {
		return placement{}, err
	}
	if !decided {
		resuming, queue, derr = e.slotDisposition(ctx, root, be, o, slot, id)
	}
	if derr != nil {
		return placement{}, derr
	}
	return placement{reg: reg, slot: slot, resuming: resuming, queue: queue}, nil
}

// claimIn is what claimsRefusal reads. The closures are the lookups it makes
// only when its earlier checks pass, so a refusal never pays for a later one.
type claimIn struct {
	agent        string
	lead         *backlog.Item
	members      []*backlog.Item
	queued       *worker.QueuedPR // the lead's PR in review on another slot, nil for none
	resuming     bool
	reclaimStale string
	staleClaim   func() error
	acceptOpenPR bool
	openPRs      func() (map[tracker.IssueKey]tracker.PR, error)
	acceptCross  bool
	crossCheck   func(backlog.Item) ([]string, error)
}

// claimsRefusal refuses an issue another claim already covers: a PR in review,
// a stale claim to reclaim, an open PR, or a tie across milestones. hasOpenPR
// is as in openPRClaims.
func claimsRefusal(in claimIn) (hasOpenPR bool, err error) {
	it := in.lead
	if q := in.queued; q != nil && !in.resuming {
		return false, blocked(blockedby.Claimed, "%s has PR %s in review (from %s): rota round transfer %s --to <slot> picks it up", it.ID, q.PR, q.From, it.ID)
	}
	if in.reclaimStale != "" {
		if err := in.staleClaim(); err != nil {
			return false, err
		}
	}
	if !in.resuming {
		openPR, listErr := in.openPRs()
		if hasOpenPR, err = openPRClaims(openPR, listErr, in.acceptOpenPR, in.agent, it, in.members); err != nil {
			return false, err
		}
	}
	if !in.acceptCross && !in.resuming {
		if err := checkCrossMilestone(in.crossCheck, append([]*backlog.Item{it}, in.members...)); err != nil {
			return false, err
		}
	}
	return hasOpenPR, nil
}

// checkClaims gathers the claim inputs from the board and runs claimsRefusal.
func (e Env) checkClaims(ctx context.Context, root string, be Board, o AssignOpts, agent string, it *backlog.Item, members []*backlog.Item, reg worker.Registry, bo *bestOfRun, resuming bool) (hasOpenPR bool, err error) {
	cross := &crossMilestones{root: root, be: be}
	return claimsRefusal(claimIn{
		agent: agent, lead: it, members: members, resuming: resuming,
		queued:       inReview(reg, it.ID, bo),
		reclaimStale: o.ReclaimStale,
		staleClaim:   func() error { return e.checkStaleClaim(ctx, root, be, it.IssueKey(), o.ReclaimStale) },
		acceptOpenPR: o.AcceptOpenPR,
		// An attempt of a best-of:2 issue ignores the PR of its sibling's branch.
		openPRs: func() (map[tracker.IssueKey]tracker.PR, error) {
			return e.openPRIssuesExcept(ctx, be, bo.otherBranch())
		},
		acceptCross: o.AcceptCrossMilestone,
		crossCheck:  cross.check,
	})
}
