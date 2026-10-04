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
	"fmt"
	"sort"
	"strings"
)

// DefaultCap is round.autopilotCap: the most assigns, and the most merges, one
// tick does.
const DefaultCap = 3

// Slot states that wait on the orchestrator, never on the autopilot.
var attention = map[string]bool{"blocked": true, "needs-permission": true, "limited": true, "dead": true, "unknown": true}

// Slot is the registry's view of one roster slot.
type Slot struct{ Name, State, Issue, PR string }

// Candidate is an item the round's scope allows. Ready is every readiness
// check, the file-overlap one included.
type Candidate struct {
	ID    string
	Ready bool
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
	// Merge gates the targets (slots or PR refs): one is a gate, several a
	// train.
	Merge func(ctx context.Context, targets []string) ([]Merged, error)
	// Candidates are the assignable items in backlog order.
	Candidates func(ctx context.Context) ([]Candidate, error)
	// Assign hands an item to the first idle slot and returns its name. A
	// refusal (not ready after all, no slot) is a *Refusal, not a failure.
	Assign func(ctx context.Context, id string) (agent string, err error)
	// Review mints the architecture-review items (#53) when a review is due,
	// at the round.architectureEvery threshold or because a slot is idle with
	// nothing assignable, and returns their ids. nil means the round has no
	// review. The minted items are assigned first, within the same cap.
	Review func(ctx context.Context) (minted []string, err error)
	// Audit records one action in the audit log.
	Audit func(Action)
	// Held is the targets a previous tick's merge failed on and a person has
	// not cleared; Remember stores the new set and the keys reported so far.
	Held     map[string]string
	Reported map[string]bool
}

// Refusal is Assign saying no without anything having gone wrong.
type Refusal struct{ Why string }

func (r *Refusal) Error() string { return r.Why }

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
	Did      []Action
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
		case attention[s.State]:
			why := s.State
			if s.Issue != "" {
				why += " on " + s.Issue
			}
			r.NeedsYou = append(r.NeedsYou, Item{s.State, s.Name, why})
		case s.State == "done" && s.PR != "":
			targets = append(targets, s.Name)
		}
	}
	if e.Queued != nil {
		targets = append(targets, e.Queued()...)
	}

	if err := e.merge(ctx, &r, targets); err != nil {
		return r, err
	}
	minted, err := e.review(ctx, &r)
	if err != nil {
		return r, err
	}
	if err := e.assign(ctx, &r, minted); err != nil {
		return r, err
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
	out, err := e.Merge(ctx, run)
	if err != nil {
		return err
	}
	for _, m := range out {
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

// review mints the due architecture reviews. A failed mint is the
// orchestrator's to look at, not a reason to stop the tick's other steps.
func (e Env) review(ctx context.Context, r *Result) ([]string, error) {
	if e.Review == nil {
		return nil, nil
	}
	ids, err := e.Review(ctx)
	if err != nil {
		r.NeedsYou = append(r.NeedsYou, Item{"architecture-review", "mint", firstNonEmpty(firstLine(err.Error()), "the review could not be minted")})
	}
	for _, id := range ids {
		e.audit(r, Action{"mint", id, "architecture review"})
	}
	return ids, nil
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return l
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
		if !contains(minted, c.ID) {
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
		agent, err := e.Assign(ctx, c.ID)
		var ref *Refusal
		switch {
		case err == nil:
			e.audit(r, Action{"assign", c.ID, "to " + agent})
			n--
		case asRefusal(err, &ref):
			// Not ready after all, or every slot filled meanwhile: try the next.
		default:
			return fmt.Errorf("assign %s: %w", c.ID, err)
		}
	}
	return nil
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func asRefusal(err error, out **Refusal) bool {
	r, ok := err.(*Refusal)
	if ok {
		*out = r
	}
	return ok
}

func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
