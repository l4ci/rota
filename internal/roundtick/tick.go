// Package roundtick is one pass of the round autopilot (#93): the mechanical
// steps of a round, done without the orchestrator spending a turn on them.
// A tick repairs safe drift, merges finished PRs behind the gate, and hands
// ready candidates to idle slots. It never decides anything a person would
// notice: a blocked worker, a failed gate, a merge policy that asks for a
// human, drift it cannot repair all come back in NeedsYou for the orchestrator.
//
// Everything a tick touches comes in through Env, so the tests need no host,
// forge or git. The CLI (`rota round tick`, `rota round watch --autopilot`)
// wires the real verbs.
package roundtick

import (
	"context"
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/worker"
	"slices"
	"sort"
	"strings"
)

// DefaultCap is round.autopilotCap: the most assigns, and the most merges, one
// tick does.
const DefaultCap = 3

// Slot is the registry's view of one roster slot.
type Slot struct{ Name, State, Issue, PR string }

// Candidate is an item the round's scope allows. Ready is every readiness
// check, the file-overlap one included.
type Candidate struct {
	ID    string
	Ready bool
}

// GateOutcome is what the gate adapter reports for one target. An empty
// Verdict on a failure reads as "gate-error".
type GateOutcome struct {
	Landed  bool
	Verdict string
	Detail  string
}

// TrainOutcome is what the train adapter reports for several targets. Landed
// are the members that merged, Culprit the member that broke the train, if
// the train names one.
type TrainOutcome struct {
	Landed  []string
	Culprit string
	Verdict string
	Detail  string
	// Done is true when the whole train landed.
	Done bool
}

// Merged is what the merge step reports for one target.
type Merged struct {
	Target string
	Landed bool
	// Hold is set for a failure only a person can clear, so the next tick does
	// not re-run the checks on it. A stale or provenance bounce is not held:
	// the worker fixes it and the PR comes back.
	Hold bool
	// Skip is a member that did not land through no fault of its own (a train
	// stopped at another member): the next tick tries it again.
	Skip    bool
	Verdict string
	Detail  string
}

// ReviewOutcome is what the review loop did for one done slot. The zero value
// means no review input was waiting. Relayed means the input went back to the
// worker, which is busy again; Pending means it waits on the orchestrator (the
// loop is manual, the item is at the bounce cap, or the relay failed), with
// Detail saying why.
type ReviewOutcome struct {
	Relayed, Pending bool
	// Hold keeps the slot from merging this tick: a Pending outcome under
	// round.reviewLoop auto (the item is at the cap, the relay or the poll
	// failed). Under manual a Pending outcome is a report only; the gate runs.
	// Hold without Pending holds the slot silently: the poll failed, and the
	// caller has already warned.
	Hold   bool
	Detail string
}

// Env is the outside world of a tick.
type Env struct {
	// Cap bounds assigns and merges per tick; <= 0 means DefaultCap.
	Cap int
	// HumanMerge is true when ship.mergeApproval is not none: the autopilot
	// then merges nothing.
	HumanMerge bool
	// Slots lists the roster from the registry; Queued the PR refs waiting in
	// its review queue.
	Slots  func() []Slot
	Queued func() []string
	// Reconcile applies the safe repairs and returns what it repaired and the
	// drift it leaves.
	Reconcile func(ctx context.Context) (repaired, drift []string, err error)
	// BaseOf names the branch a target (a slot or PR ref) merges into. Targets
	// sharing a base are gated together: one is a gate, several a train.
	BaseOf func(target string) string
	// Gate lands one target into base; Train lands several, in order.
	Gate  func(ctx context.Context, target, base string) GateOutcome
	Train func(ctx context.Context, targets []string, base string) TrainOutcome
	// Candidates are the assignable items in backlog order.
	Candidates func(ctx context.Context) ([]Candidate, error)
	// Assign hands an item to the first idle slot and returns the slot names
	// (two for a best-of:2 issue). A *round.BlockedError (not ready after all,
	// no slot) is a refusal, not a failure; "no round" is a failure.
	Assign func(ctx context.Context, id string) (agents []string, err error)
	// Review mints the architecture-review items (#53) when a review is due,
	// at the round.architectureEvery threshold or because a slot is idle with
	// nothing assignable, and returns their ids. nil means the round has no
	// review. The minted items are assigned first, within the same cap.
	Review func(ctx context.Context) (minted []string, err error)
	// ReviewLoop checks a done slot's PR for review input (round.reviewLoop);
	// nil means the round has no review loop. A slot it relays, or holds, is not
	// merged this tick; one it only reports (manual) is gated as before.
	ReviewLoop func(ctx context.Context, s Slot) ReviewOutcome
	// Capped returns why the round may not fill slots now (every account is
	// cooling down), "" when it may. nil means no quota cap. A capped tick
	// mints and assigns nothing and says why in Result.Capped; it merges and
	// repairs as usual, and fills again once Capped returns "".
	Capped func(ctx context.Context) string
	// Audit records one action in the audit log.
	Audit func(Action)
	// Held is the targets a previous tick's merge failed on and a person has
	// not cleared; Remember stores the new set and the keys reported so far.
	Held     map[string]string
	Reported map[string]bool
}

// Action is something the tick did.
type Action struct{ Action, Target, Detail string }

// Item is something the orchestrator has to look at.
type Item struct{ Kind, Target, Why string }

// Key identifies an item across ticks.
func (i Item) Key() string { return i.Kind + " " + i.Target }

// Result is a tick's outcome. New are the NeedsYou items not reported by an
// earlier tick: the watch wakes the orchestrator for those, not for the same
// blocked slot every pass.
type Result struct {
	Did []Action
	// Capped is why the tick filled no slot: the quota cap (Env.Capped).
	Capped   string
	NeedsYou []Item
	New      []Item
	// Held and Reported are the state the next tick starts from.
	Held     map[string]string
	Reported map[string]bool
}

func (e Env) cap() int {
	if e.Cap <= 0 {
		return DefaultCap
	}
	return e.Cap
}

func (e Env) audit(r *Result, a Action) {
	r.Did = append(r.Did, a)
	if e.Audit != nil {
		e.Audit(a)
	}
}

// Run does one tick: reconcile, merge, review, assign.
//
// The merge comes before the assign so the next assignment branches from the
// gated base (rota-orchestrate, section 6), not from before the merge.
func Run(ctx context.Context, e Env) (Result, error) {
	r := Result{Held: map[string]string{}, Reported: map[string]bool{}}

	repaired, drift, err := e.Reconcile(ctx)
	if err != nil {
		return r, err
	}
	for _, f := range repaired {
		e.audit(&r, Action{"reconcile", f, "repaired"})
	}
	for _, f := range drift {
		r.NeedsYou = append(r.NeedsYou, Item{"drift", f, "drift the autopilot does not repair"})
	}

	slots := e.Slots()
	var targets []string
	for _, s := range slots {
		switch {
		case worker.NeedsAttention(s.State):
			why := s.State
			if s.Issue != "" {
				why += " on " + s.Issue
			}
			r.NeedsYou = append(r.NeedsYou, Item{s.State, s.Name, why})
		case s.State == "done" && s.PR != "":
			if e.ReviewLoop != nil {
				switch out := e.ReviewLoop(ctx, s); {
				case out.Relayed:
					e.audit(&r, Action{"review-relay", s.Name, out.Detail})
					continue
				case out.Pending:
					r.NeedsYou = append(r.NeedsYou, Item{"review", s.Name, out.Detail})
					if out.Hold {
						continue
					}
				case out.Hold: // a failed poll: held without an item, the caller warned
					continue
				}
			}
			targets = append(targets, s.Name)
		}
	}
	if e.Queued != nil {
		targets = append(targets, e.Queued()...)
	}

	if err := e.merge(ctx, &r, targets); err != nil {
		return r, err
	}
	if e.Capped != nil {
		r.Capped = e.Capped(ctx)
	}
	if r.Capped == "" { // capped: a slot filled now would park at once
		minted, err := e.review(ctx, &r)
		if err != nil {
			return r, err
		}
		if err := e.assign(ctx, &r, minted); err != nil {
			return r, err
		}
	}

	sort.SliceStable(r.NeedsYou, func(i, j int) bool { return r.NeedsYou[i].Key() < r.NeedsYou[j].Key() })
	for _, it := range r.NeedsYou {
		r.Reported[it.Key()] = true
		if !e.Reported[it.Key()] {
			r.New = append(r.New, it)
		}
	}
	return r, nil
}

func (e Env) merge(ctx context.Context, r *Result, targets []string) error {
	if len(targets) == 0 {
		return nil
	}
	if e.HumanMerge {
		for _, t := range targets {
			r.NeedsYou = append(r.NeedsYou, Item{"merge", t, "ship.mergeApproval asks for a person; the autopilot merges nothing"})
		}
		return nil
	}
	var run []string
	for _, t := range targets {
		if why, held := e.Held[t]; held {
			r.Held[t] = why
			r.NeedsYou = append(r.NeedsYou, Item{"gate-failed", t, why})
			continue
		}
		if len(run) < e.cap() {
			run = append(run, t)
		}
	}
	if len(run) == 0 {
		return nil
	}
	for _, m := range e.mergeAll(ctx, run) {
		switch {
		case m.Skip:
		case m.Landed:
			e.audit(r, Action{"merge", m.Target, m.Detail})
		case m.Verdict == "":
			r.NeedsYou = append(r.NeedsYou, Item{"gate-failed", m.Target, firstNonEmpty(m.Detail, "the gate did not pass")})
		default:
			why := m.Verdict
			if m.Detail != "" {
				why += ": " + m.Detail
			}
			if m.Hold {
				r.Held[m.Target] = why
			}
			r.NeedsYou = append(r.NeedsYou, Item{"gate-failed", m.Target, why})
		}
	}
	return nil
}

// mergeAll groups the targets by the base each merges into, in first-seen
// order: a lone target is a gate, several a train. A target that did not land
// is reported with its verdict; whether it is held for a person is the
// verdict's call: a stale or provenance bounce goes back to the worker and
// returns, any other failure waits for a person.
func (e Env) mergeAll(ctx context.Context, targets []string) []Merged {
	var order []string
	groups := map[string][]string{}
	for _, t := range targets {
		b := e.BaseOf(t)
		if _, ok := groups[b]; !ok {
			order = append(order, b)
		}
		groups[b] = append(groups[b], t)
	}
	var out []Merged
	for _, b := range order {
		if ts := groups[b]; len(ts) == 1 {
			out = append(out, e.gate(ctx, ts[0], b))
		} else {
			out = append(out, e.train(ctx, ts, b)...)
		}
	}
	return out
}

func (e Env) gate(ctx context.Context, target, base string) Merged {
	g := e.Gate(ctx, target, base)
	if g.Landed {
		return Merged{Target: target, Landed: true, Detail: "into " + base}
	}
	v := g.Verdict
	if v == "" {
		v = "gate-error"
	}
	return Merged{Target: target, Verdict: v, Hold: holdable(v), Detail: g.Detail}
}

func (e Env) train(ctx context.Context, targets []string, base string) []Merged {
	tr := e.Train(ctx, targets, base)
	out := make([]Merged, 0, len(targets))
	if tr.Done {
		for _, t := range targets {
			out = append(out, Merged{Target: t, Landed: true, Detail: "train into " + base})
		}
		return out
	}
	v := tr.Verdict
	if v == "" {
		v = "train-error"
	}
	for _, t := range targets {
		m := Merged{Target: t}
		switch {
		case slices.Contains(tr.Landed, t):
			m.Landed, m.Detail = true, "train into "+base
		case v == "base-moved":
			m.Skip = true // nothing landed; the base moved under the train
		case tr.Culprit != "" && t != tr.Culprit && !strings.Contains(tr.Culprit, t):
			m.Skip = true // another member broke the train: retried without it
		default:
			m.Verdict, m.Hold, m.Detail = v, holdable(v), tr.Detail
		}
		out = append(out, m)
	}
	return out
}

// holdable reports whether a refused target waits for a person. A stale or
// provenance-failed PR goes back to its worker; a best-of:2 attempt no pick
// names clears itself once the orchestrator picks.
func holdable(verdict string) bool {
	return verdict != worker.GateStale && verdict != worker.GateProvenanceFail && verdict != worker.GateBestOfUnpicked
}

// review mints the due architecture reviews. A failed mint is the
// orchestrator's to look at, not a reason to stop the tick's other steps.
func (e Env) review(ctx context.Context, r *Result) ([]string, error) {
	if e.Review == nil {
		return nil, nil
	}
	ids, err := e.Review(ctx)
	if err != nil {
		r.NeedsYou = append(r.NeedsYou, Item{"architecture-review", "mint", firstNonEmpty(strutil.FirstLine(err.Error()), "the review could not be minted")})
	}
	for _, id := range ids {
		e.audit(r, Action{"mint", id, "architecture review"})
	}
	return ids, nil
}

func (e Env) assign(ctx context.Context, r *Result, minted []string) error {
	idle := 0
	for _, s := range e.Slots() {
		if s.State == "idle" || s.State == "" {
			if s.Issue == "" {
				idle++
			}
		}
	}
	n := idle
	if c := e.cap(); n > c {
		n = c
	}
	if n == 0 {
		return nil
	}
	// The minted reviews go first: they are the reason the tick minted them.
	// One that is refused stays a candidate for a later tick.
	queue := make([]Candidate, 0, len(minted))
	for _, id := range minted {
		queue = append(queue, Candidate{ID: id, Ready: true})
	}
	cands, err := e.Candidates(ctx)
	if err != nil {
		return err
	}
	for _, c := range cands {
		if !slices.Contains(minted, c.ID) {
			queue = append(queue, c)
		}
	}
	for _, c := range queue {
		if n == 0 {
			break
		}
		if !c.Ready {
			continue
		}
		agents, err := e.Assign(ctx, c.ID)
		switch {
		case err == nil:
			e.audit(r, Action{"assign", c.ID, "to " + strings.Join(agents, " and ")})
			n--
		case isRefusal(err):
			// Not ready after all, or every slot filled meanwhile: try the next.
			// A quota refusal is the cap showing through a pool the cap check
			// left open (Claude workers beside Codex logins): say why.
			var blk *round.BlockedError
			if errors.As(err, &blk) && blk.By == round.BlockQuota && r.Capped == "" {
				r.Capped = blk.Msg
			}
		default:
			return fmt.Errorf("assign %s: %w", c.ID, err)
		}
	}
	return nil
}

// isRefusal reports whether err is Assign declining without anything having
// gone wrong: a *round.BlockedError other than "no round", which means the
// autopilot has no round to work for.
func isRefusal(err error) bool {
	var blk *round.BlockedError
	return errors.As(err, &blk) && blk.By != round.BlockNoRound
}

func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
