package roundtick

import (
	"context"
	"fmt"
	"slices"

	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/worker"
)

// aheadOutcome is what assign-ahead did: the merge targets with each assigned
// slot swapped for its queued PR, how much of the cap it used, the slots it
// took and the items it gave (which the later steps leave alone), and the
// swapped refs mapped back to their slot names.
type aheadOutcome struct {
	targets []string
	used    int
	taken   []string
	given   []string
	alias   map[string]string
}

// assignAhead (#4247) hands each done slot whose PR is a merge target this
// tick the next ready candidate, before the merge, so its worker does not wait
// for the gate or train that lands the PR. `round assign --agent` parks the
// slot and queues its PR; the slot's name in targets then becomes that PR's
// ref, so the merge lands the queued PR and never gates the slot's new
// branch. A refused assign leaves the slot a slot target. When the queue holds
// no record of the slot's PR after the assign, the target is dropped for this
// tick and reported: gating the slot by name would gate the new branch.
//
// The cap, round.batch and the quota cap limit it as they limit the assign
// step. It runs only when the merge step would land the target: never on a
// support machine or under a merge policy that asks for a person.
func (e Env) assignAhead(ctx context.Context, r *Result, slots []Slot, targets []string) (aheadOutcome, error) {
	out := aheadOutcome{targets: targets}
	if e.AssignTo == nil || e.Role == roundcfg.RoleSupport || e.humanMerge() {
		return out, nil
	}
	slots = slices.Clone(slots) // the assigns below change the registry the snapshot came from
	due := e.aheadSlots(ctx, r, slots, targets)
	if len(due) == 0 {
		return out, nil
	}
	if e.Capped != nil && e.Capped(ctx) != "" || e.batchFull(ctx) {
		return out, nil
	}
	n := e.cap()
	var q *pickQueue
	for _, s := range due {
		if n == 0 {
			break
		}
		if slices.Contains(out.taken, s.Name) { // the second slot of a best-of:2 assign
			continue
		}
		if q == nil {
			var err error
			if q, err = e.pickQueue(ctx, r, nil, nil, false); err != nil {
				return out, err
			}
		}
		gave, agents, stop, err := e.aheadOne(ctx, r, q, s, slots, &out)
		if err != nil {
			return out, err
		}
		if len(gave) > 0 {
			n--
			out.used++
			out.taken = append(out.taken, agents...)
			out.given = append(out.given, gave...)
		}
		if stop {
			break
		}
	}
	return out, nil
}

// aheadSlots are the slot targets assign-ahead may fill: done, with a PR, not
// held at a failed head (that PR goes back to its own worker, as before), and
// not waiting for train-fill readiness.
func (e Env) aheadSlots(ctx context.Context, r *Result, slots []Slot, targets []string) []Slot {
	var due []Slot
	for _, s := range slots {
		if !slices.Contains(targets, s.Name) || !worker.IsDone(s.State) || s.PR == "" {
			continue
		}
		if !e.targetDue(ctx, r.FailedHeads, s.Name).due() {
			continue
		}
		due = append(due, s)
	}
	return due
}

// aheadOne hands slot s the first queued item it takes and returns its id
// with the bundle members that rode along, nothing when none went, and the
// slots the assign took (two for a best-of:2 issue; each that was a merge
// target is swapped for its queued PR). A refusal about the item tries the next one; a refusal about
// the slot (busy, no login, draining) leaves the slot alone; one that holds
// every later assign (the batch, the quota, a running gate) also stops the
// step: stop.
func (e Env) aheadOne(ctx context.Context, r *Result, q *pickQueue, s Slot, slots []Slot, out *aheadOutcome) (gave, agents []string, stop bool, err error) {
	for i := 0; i < len(q.ids); i++ {
		id := q.ids[i]
		if q.members[id] || slices.Contains(out.given, id) {
			continue
		}
		b, bundled := q.bundles[id]
		detail := fmt.Sprintf("to %s ahead of the merge of %s", s.Name, s.PR)
		if bundled {
			detail += "; " + bundleDetail(b)
		}
		r.propose(Action{Action: "assign", Target: id, Detail: detail}, "claim, parking the done slot, queuing its PR and dispatch preflight")
		if e.Observe {
			e.swapTarget(r, s, out, true)
			return append([]string{id}, b.Members...), []string{s.Name}, false, nil
		}
		var members []string
		if bundled {
			members = b.Members
		}
		agents, err := e.AssignTo(ctx, s.Name, id, members)
		if bundled && isRefusal(err) {
			e.swapIfQueued(s, out)
			q.unbundle(b, i)
			members, detail = nil, fmt.Sprintf("to %s ahead of the merge of %s", s.Name, s.PR)
			agents, err = e.AssignTo(ctx, s.Name, id, nil)
		}
		switch {
		case err == nil:
			if !slices.Contains(agents, s.Name) {
				agents = append([]string{s.Name}, agents...)
			}
			e.audit(r, Action{Action: "assign", Target: id, Detail: detail})
			e.swapTarget(r, s, out, false)
			for _, a := range agents {
				if i := slices.IndexFunc(slots, func(o Slot) bool { return o.Name == a }); a != s.Name && i >= 0 && slots[i].PR != "" {
					e.swapTarget(r, slots[i], out, false) // a best-of's second slot, when it was a merge target
				}
			}
			return append([]string{id}, members...), agents, false, nil
		case isRefusal(err):
			// The assign may have queued the slot's PR before it refused (the
			// claim is taken after the park): land it by its ref.
			e.swapIfQueued(s, out)
			if e.fillStops(r, err) {
				return nil, nil, true, nil
			}
			if scope, _ := scopeOf(err); scope != scopeItem {
				return nil, nil, false, nil
			}
		default:
			return nil, nil, false, fmt.Errorf("assign %s to %s: %w", id, s.Name, err)
		}
	}
	return nil, nil, false, nil
}

// swapTarget replaces slot s in the merge targets with the PR the assign
// queued. Execution checks the queue first: with no record of the slot's PR
// there, the target is dropped for this tick and reported. Observation swaps
// as execution would.
func (e Env) swapTarget(r *Result, s Slot, out *aheadOutcome, observe bool) {
	i := slices.Index(out.targets, s.Name)
	if i < 0 {
		return
	}
	if observe {
		out.swap(s, i)
		return
	}
	if !e.swapIfQueued(s, out) {
		out.targets = slices.Delete(out.targets, i, i+1)
		r.NeedsYou = append(r.NeedsYou, Item{Kind: "assign-ahead", Target: s.Name, Why: fmt.Sprintf("assigned ahead, but its PR %s is not in the review queue; not landed this tick, check `rota round status`", s.PR)})
	}
}

// swapIfQueued swaps slot s in the merge targets for its PR when the review
// queue holds that PR, and says whether it did. A slot that is no target is
// left alone.
func (e Env) swapIfQueued(s Slot, out *aheadOutcome) bool {
	i := slices.Index(out.targets, s.Name)
	if i < 0 || e.Queued == nil || !slices.ContainsFunc(e.Queued(), func(ref string) bool { return samePR(ref, s.PR) }) {
		return false
	}
	out.swap(s, i)
	return true
}

// swap replaces target i, slot s, with s's PR ref.
func (out *aheadOutcome) swap(s Slot, i int) {
	if slices.Contains(out.targets, s.PR) {
		out.targets = slices.Delete(out.targets, i, i+1)
	} else {
		out.targets[i] = s.PR
	}
	if out.alias == nil {
		out.alias = map[string]string{}
	}
	out.alias[s.PR] = s.Name
}

// samePR compares two PR refs by number when both carry one.
func samePR(a, b string) bool {
	if a == b {
		return true
	}
	m, okA := worker.PRRefNumber(a)
	n, okB := worker.PRRefNumber(b)
	return okA && okB && m == n
}
