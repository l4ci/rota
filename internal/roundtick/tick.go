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
	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/gate"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/worker"
	"maps"
	"slices"
	"sort"
	"strings"
	"time"
)

// ErrStopped says the round is winding down: Run assigns and merges nothing.
var ErrStopped = errors.New("the round is stopped by `rota round wind-down`; the autopilot resumes when wind-down finishes or a wind-down verifies the base green")

// StopVerifyFailed is the stop reason of a red wind-down verify.
const StopVerifyFailed = "verify-failed"

// ErrBaseRed is ErrStopped after a wind-down verify went red: only a wind-down
// that verifies the base green resumes the autopilot.
var ErrBaseRed = fmt.Errorf("%w: the base failed the wind-down verify; fix the base, then re-run `rota round wind-down`", ErrStopped)

// DefaultCap is round.autopilotCap: the most assigns, and the most merges, one
// tick does.
const DefaultCap = 3

// Slot is the registry's view of one roster slot.
type Slot struct{ Name, State, Issue, PR string }

// Merged is what the merge step reports for one target.
type Merged struct {
	Target string
	Landed bool
	// Hold is set for a failure only a person can clear, so the next tick does
	// not re-run the checks on it. A stale or provenance refusal is not held:
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
	// Proposed is review input that observation would relay, without sending it.
	Proposed bool
	Unknown  string
	// Hold keeps the slot from merging this tick: a Pending outcome under
	// round.reviewLoop auto (the item is at the cap, the relay or the poll
	// failed). Under manual a Pending outcome is a report only; the gate runs.
	// Hold without Pending holds the slot silently: the poll failed, and the
	// caller has already warned.
	Hold   bool
	Detail string
}

// Reviewer is the adapter behind the architecture review. Facts reads the
// counter, the pending items, the idle slots, and whether any candidate is
// ready, and decides nothing; Mint creates the review items for the facts the
// tick decided on and returns their ids.
type Reviewer interface {
	Facts(ctx context.Context) (a round.Architecture, ready bool, err error)
	Mint(ctx context.Context, a round.Architecture) (minted []string, err error)
}

// Env is the outside world of a tick.
type Env struct {
	// Stopped is the round's wind-down flag (State.Stopped): Run refuses before
	// it reconciles, merges or assigns.
	Stopped bool
	// StoppedReason is State.StoppedReason: StopVerifyFailed picks ErrBaseRed.
	StoppedReason string
	// Observe runs the same pass over a read-only world: Reconcile,
	// ReviewLoop, AwaitingReview and Review.Facts inspect without writing, and
	// every action is proposed instead of taken. The adapters that execute
	// (RunGate, RunTrain, Assign, Review.Mint, FollowUp.Start, Audit) are
	// never called.
	Observe bool
	// BaseOverride wins over a recorded target base; Base is the fallback
	// when the target has none or its registry lookup fails.
	Base, BaseOverride string
	// Target reads a registry target without choosing its base.
	Target func(string) (worker.GateTarget, error)
	// Head reads the current PR identity and head SHA. An unreadable head
	// cannot authorize a retry of a failed attempt.
	Head func(context.Context, string) (string, error)
	// RunGate and RunTrain execute the shared gate/train runs, including
	// bookkeeping; the tick interprets their results and reports diagnostics.
	RunGate  func(context.Context, string, string) GateRun
	RunTrain func(context.Context, []string, string) TrainRun
	// Say writes a bookkeeping diagnostic, without a trailing newline.
	Say func(string)
	// Cap bounds assigns and merges per tick; <= 0 means DefaultCap.
	Cap int
	// TrainFill holds automatic merges until the count or age rule fires.
	// ReadySince reads the current-head review time; unknown times cannot
	// trigger the age rule. Now defaults to time.Now.
	TrainFill roundcfg.TrainFill
	// TrainReady excludes PRs that are no longer reviewed and ready from
	// configured fill decisions. Nil accepts the already selected targets.
	TrainReady func(string) bool
	ReadySince func(string) time.Time
	// FillBypass sends known PR-state refusals and ready PRs without a usable
	// review-disabled clock to an individual gate instead of waiting for fill.
	FillBypass func(string) bool
	Now        func() time.Time
	// MergeApproval is ship.mergeApproval: any mode but none (empty
	// included) asks for a person, and the autopilot then merges nothing.
	MergeApproval string
	// Role is round.role. A support machine merges nothing and calls neither
	// RunGate nor RunTrain: the queued PRs are main's.
	Role string
	// Slots lists the roster from the registry; Queued the PR refs waiting in
	// its review queue.
	Slots  func() []Slot
	Queued func() []string
	// Active names the round's active slots (round.Members). Only they count
	// toward the idle budget; nil counts every registered slot.
	Active func() []string
	// Reconcile applies the safe repairs and returns what it repaired and the
	// drift it leaves. Over a read-only world (Observe) it repairs nothing and
	// returns the repairs it would make as Proposed.
	Reconcile func(ctx context.Context) (Inspection, error)
	// Candidates are the assignable items in backlog order.
	Candidates func(ctx context.Context) ([]round.Candidate, error)
	// Assign hands an item to the first idle slot and returns the slot names
	// (two for a best-of:2 issue). A *round.BlockedError (not ready after all,
	// no slot) is a refusal, not a failure; "no round" is a failure.
	Assign func(ctx context.Context, id string) (agents []string, err error)
	// Review supplies the architecture review (#53): the facts the decision
	// rests on, and the mint. The tick decides with round.DecideReview, at the
	// round.architectureEvery threshold or because a slot is idle with nothing
	// assignable, then mints. nil means the round has no review. The minted
	// items are assigned first, within the same cap.
	Review Reviewer
	// ReviewLoop checks a done slot's PR for review input (round.reviewLoop);
	// nil means the round has no review loop. A slot it relays, or holds, is not
	// merged this tick; one it only reports (manual) is gated as before.
	ReviewLoop func(ctx context.Context, s Slot) ReviewOutcome
	// AwaitingReview says why a done slot's head still lacks the review
	// verdicts ship.review needs, "" when it has them (or needs none). Such a
	// slot is not gated: the gate checks freshness first, so a fresh done with
	// no verdicts would read as stale. nil means no such check.
	AwaitingReview func(ctx context.Context, s Slot, base string) string
	// Capped returns why the round may not fill slots now (every account is
	// cooling down), "" when it may. nil means no quota cap. A capped tick
	// mints and assigns nothing and says why in Result.Capped; it merges and
	// repairs as usual, and fills again once Capped returns "".
	Capped func(ctx context.Context) string
	// Batch is round.batch: once that many items are open (OpenItems), nothing
	// is minted or assigned. <= 0 means no limit. Merges and repairs run as usual.
	Batch     int
	OpenItems func(ctx context.Context) int
	// FollowUp is the follow-up step (#2580): queued PRs a person asked to
	// continue start before backlog work. The zero value has none.
	FollowUp FollowUpSources
	// Audit records one action in the audit log.
	Audit func(Action)
	// Held is the targets a previous tick's merge failed on and a person has
	// not cleared; Remember stores the new set and the keys reported so far.
	Held     map[string]string
	Reported map[string]bool
	// Filling is the last base/count/max key, for changed-only output.
	Filling map[string]string
	// FailedHeads remembers attempted failures across worker state changes.
	// Unlike legacy Held entries, these clear only when the PR head changes.
	FailedHeads map[string]FailedHead
}

// FailedHead binds a failed attempt to the PR identity and head it judged.
type FailedHead struct {
	Head string `json:"head"`
	Why  string `json:"why"`
}

// ActionMerge is the Action of a PR the tick landed; the watch wakes on it
// under train fill.
const ActionMerge = "merge"

// Action is something the tick did.
type Action struct {
	Action, Target, Detail string
	// Drift is the reconcile finding kind behind the action, "" for other actions.
	Drift DriftKind
}

// Item is something the orchestrator has to look at.
type Item struct {
	Kind, Target, Why string
	// Drift is the reconcile finding kind of a drift item, "" for other items.
	Drift DriftKind
}

// Key identifies an item across ticks.
func (i Item) Key() string { return i.Kind + " " + i.Target }

// Result is a tick's outcome. New are the NeedsYou items not reported by an
// earlier tick: the watch wakes the orchestrator for those, not for the same
// blocked slot every pass.
type Result struct {
	Observed bool
	Proposed []Proposal
	Unknown  []Item
	Did      []Action
	// Holding contains changed, non-actionable train fill status lines.
	Holding []string
	// FillStatus is the current rendered progress, including observed time.
	// It is available for status and heartbeats without being a wake reason.
	FillStatus []string
	Filling    map[string]string
	// Capped is why the tick filled no slot: the quota cap (Env.Capped).
	Capped string
	// BatchFull is true when round.batch items are open: the tick assigned
	// nothing more, and minted no review.
	BatchFull bool
	// Deferred says why a merge or assign yielded to running or queued landing
	// work. The next tick retries; the reason names the holder when known.
	Deferred string
	// Holds are due PRs the tick did not start, one line each with the reason:
	// the autopilot says why it holds instead of leaving the train nudge
	// unanswered. Not attention items: they never wake the watch.
	Holds    []Item
	NeedsYou []Item
	New      []Item
	// Held and Reported are the state the next tick starts from.
	Held        map[string]string
	Reported    map[string]bool
	FailedHeads map[string]FailedHead
}

func (e Env) cap() int {
	if e.Cap <= 0 {
		return DefaultCap
	}
	return e.Cap
}

// humanMerge is true when the merge policy asks for a person. An unset policy
// counts as asking: only an explicit none lets the autopilot merge.
func (e Env) humanMerge() bool {
	return e.MergeApproval != gate.MergeNone
}

func (e Env) batchFull(ctx context.Context) bool {
	return e.Batch > 0 && e.OpenItems != nil && e.OpenItems(ctx) >= e.Batch
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
	if e.Stopped {
		if e.StoppedReason == StopVerifyFailed {
			return Result{}, ErrBaseRed
		}
		return Result{}, ErrStopped
	}
	r := Result{Observed: e.Observe, Held: map[string]string{}, Reported: map[string]bool{}, FailedHeads: maps.Clone(e.FailedHeads), Filling: map[string]string{}}
	if r.FailedHeads == nil {
		r.FailedHeads = map[string]FailedHead{}
	}
	if e.Head != nil {
		// Older ticks persisted every refusal, including temporary conditions.
		// Do not keep such entries pinned to an otherwise unchanged head.
		for target, prev := range r.FailedHeads {
			v, _, _ := strings.Cut(prev.Why, ":")
			if !failedHeadVerdict(v) {
				delete(r.FailedHeads, target)
			}
		}
	}

	inspection, err := e.Reconcile(ctx)
	if err != nil {
		return r, err
	}
	if e.Observe {
		r.Unknown = append(r.Unknown, inspection.Unknown...)
	}
	for _, f := range inspection.Proposed {
		r.propose(Action{Action: "reconcile", Target: f.String(), Detail: "safe repair selected", Drift: f.Kind}, "repair prerequisites and result")
	}
	repaired, drift := inspection.Repaired, inspection.Drift
	for _, f := range repaired {
		e.audit(&r, Action{Action: "reconcile", Target: f.String(), Detail: "repaired", Drift: f.Kind})
	}
	for _, f := range drift {
		r.NeedsYou = append(r.NeedsYou, Item{Kind: "drift", Target: f.String(), Why: "drift the autopilot does not repair", Drift: f.Kind})
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
			r.NeedsYou = append(r.NeedsYou, Item{Kind: s.State, Target: s.Name, Why: why})
		case worker.IsDone(s.State) && s.PR != "":
			if e.ReviewLoop != nil {
				out := e.ReviewLoop(ctx, s)
				if out.Unknown != "" {
					r.Unknown = append(r.Unknown, Item{Kind: "review", Target: s.Name, Why: out.Unknown})
				}
				switch {
				case out.Proposed:
					r.propose(Action{Action: "review-relay", Target: s.Name, Detail: out.Detail}, "bounce cap, relay authorization and delivery")
					continue
				case out.Relayed:
					e.audit(&r, Action{Action: "review-relay", Target: s.Name, Detail: out.Detail})
					continue
				case out.Pending:
					r.NeedsYou = append(r.NeedsYou, Item{Kind: "review", Target: s.Name, Why: out.Detail})
					if out.Hold {
						continue
					}
				case out.Hold: // a failed poll: held without an item, the caller warned
					continue
				}
			}
			if e.AwaitingReview != nil {
				if e.Observe {
					r.Unknown = append(r.Unknown, Item{Kind: "awaiting-review", Target: s.Name, Why: "review verdicts read from local records and last-fetched refs; execution fetches and recovers PR verdict reports"})
				}
				if why := e.AwaitingReview(ctx, s, e.targetBase(s.Name)); why != "" {
					r.NeedsYou = append(r.NeedsYou, Item{Kind: "awaiting-review", Target: s.Name, Why: why})
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
	if e.batchFull(ctx) {
		r.BatchFull = true
	}
	// A follow-up reopens no item, so a full batch does not hold it.
	started, taken, err := e.followUps(ctx, &r)
	if err != nil {
		return r, err
	}
	if r.Capped == "" && !r.BatchFull { // capped: a slot filled now would park at once
		minted, err := e.review(ctx, &r)
		if err != nil {
			return r, err
		}
		if err := e.assign(ctx, &r, minted, started, taken); err != nil {
			return r, err
		}
	}

	sort.SliceStable(r.NeedsYou, func(i, j int) bool { return r.NeedsYou[i].Key() < r.NeedsYou[j].Key() })
	for _, it := range r.NeedsYou {
		key := it.Key()
		if it.Drift == round.BranchNoPR {
			name := strings.TrimPrefix(it.Target, string(it.Drift)+" ")
			// A missing PR is news again when its slot changes state or
			// takes another issue, even while the finding stays present.
			for _, s := range slots {
				if s.Name == name {
					key += " " + s.State + " " + s.Issue
					break
				}
			}
		}
		r.Reported[key] = true
		if !e.Reported[key] {
			r.New = append(r.New, it)
		}
	}
	if e.Observe {
		r.Held, r.FailedHeads, r.Reported, r.Filling = maps.Clone(e.Held), maps.Clone(e.FailedHeads), maps.Clone(e.Reported), maps.Clone(e.Filling)
		r.New = nil
		if len(r.Proposed) > 0 {
			r.Unknown = append(r.Unknown, Item{Kind: "downstream", Target: "round", Why: "later proposals use the current snapshot; repairs, relays, landings and minted items may change or enable later actions"})
		}
	}
	return r, nil
}

func (e Env) merge(ctx context.Context, r *Result, targets []string) error {
	if len(targets) == 0 {
		return nil
	}
	if e.Role == roundcfg.RoleSupport {
		for _, t := range targets {
			r.hold(t, "support role: the main orchestrator lands it")
		}
		return nil
	}
	if e.humanMerge() {
		for _, t := range targets {
			r.NeedsYou = append(r.NeedsYou, Item{Kind: "merge", Target: t, Why: "ship.mergeApproval asks for a person; the autopilot merges nothing"})
		}
		return nil
	}
	var run []string
	heads := map[string]string{}
	for _, t := range targets {
		why, held := HoldReason(e.Held, r.FailedHeads, t, "")
		if e.Head != nil {
			head, err := e.Head(ctx, t)
			if err != nil || head == "" {
				detail := "cannot read PR head; not retrying the gate"
				if err != nil {
					detail += ": " + strutil.FirstLine(err.Error())
				}
				r.NeedsYou = append(r.NeedsYou, Item{Kind: "gate-failed", Target: t, Why: detail})
				continue
			}
			heads[t] = head
			why, held = HoldReason(e.Held, r.FailedHeads, t, head)
			if prev, ok := r.FailedHeads[t]; ok && prev.Head != head {
				delete(r.FailedHeads, t)
			}
			if held {
				for old, prev := range r.FailedHeads {
					if old != t && prev.Head == head {
						delete(r.FailedHeads, old)
					}
				}
				r.FailedHeads[t] = FailedHead{Head: head, Why: why}
			}
		}
		if held {
			r.Held[t] = why
			r.NeedsYou = append(r.NeedsYou, heldItem(t, why, e.Head != nil))
			continue
		}
		if e.TrainFill.Enabled() && e.TrainReady != nil && !e.TrainReady(t) && (e.FillBypass == nil || !e.FillBypass(t)) {
			r.NeedsYou = append(r.NeedsYou, Item{Kind: "awaiting-review", Target: t, Why: "PR is not ready for train fill; waiting for current-head review readiness"})
			continue
		}
		if e.TrainFill.Enabled() || len(run) < e.cap() {
			run = append(run, t)
		} else {
			r.hold(t, capHold(e.cap()))
		}
	}
	if len(run) == 0 {
		return nil
	}
	for _, m := range e.mergeAll(ctx, r, run) {
		switch {
		case m.Skip:
			r.hold(m.Target, strutil.FirstNonEmpty(m.Detail, "did not land through no fault of its own")+"; retried next tick")
		case m.Verdict == worker.GateRunning:
			r.Deferred = "gate running, deferred: " + m.Detail
		case m.Landed:
			e.audit(r, Action{Action: ActionMerge, Target: m.Target, Detail: m.Detail})
		case m.Verdict == "":
			r.NeedsYou = append(r.NeedsYou, Item{Kind: "gate-failed", Target: m.Target, Why: strutil.FirstNonEmpty(m.Detail, "the gate did not pass")})
		default:
			why := m.Verdict
			if m.Detail != "" {
				why += ": " + m.Detail
			}
			if e.Head != nil && failedHeadVerdict(m.Verdict) || e.Head == nil && m.Hold {
				r.Held[m.Target] = why
				if head := heads[m.Target]; head != "" {
					r.FailedHeads[m.Target] = FailedHead{Head: head, Why: why}
				}
			}
			r.NeedsYou = append(r.NeedsYou, heldItem(m.Target, why, e.Head != nil && failedHeadVerdict(m.Verdict)))
		}
	}
	return nil
}

func capHold(n int) string {
	return fmt.Sprintf("over round.autopilotCap (%d) merges this tick; next tick", n)
}

// hold records why a due target was not started this tick.
func (r *Result) hold(target, why string) {
	r.Holds = append(r.Holds, Item{Kind: "train-hold", Target: target, Why: why})
}

const heldHeadHint = "; held at PR head; push a new head to retry"

// heldItem reports a target the gate or train refused; why starts with the
// verdict. An interaction is the orchestrator's to fix, not a bounce: its why
// already says what to do, so it carries no push-a-new-head hint.
func heldItem(target, why string, atHead bool) Item {
	if v, _, _ := strings.Cut(why, ":"); v == worker.GateInteraction {
		return Item{Kind: "train-interaction", Target: target, Why: why}
	}
	if atHead {
		why += heldHeadHint
	}
	return Item{Kind: "gate-failed", Target: target, Why: why}
}

// failedHeadVerdict identifies results that judged the head's content. A CI
// failure and a failed train verification both use GateVerifyFailed; merge
// conflicts use GateStale; members red only together use GateInteraction. Refusals and infrastructure errors can clear without
// a push, so they are reported for this tick and retried on the next one.
func failedHeadVerdict(v string) bool {
	switch v {
	case worker.GateStale, worker.GateVerifyFailed, worker.GateVacuousTests, worker.GateInteraction:
		return true
	default:
		return false
	}
}

// mergeAll groups the targets by the base each merges into, in first-seen
// order: a lone target is a gate, several a train. A target that did not land
// is reported with its verdict; whether it is held for a person is the
// verdict's call: a stale or provenance refusal goes back to the worker uncounted and
// returns, any other failure waits for a person.
func (e Env) mergeAll(ctx context.Context, r *Result, targets []string) []Merged {
	var order []string
	groups := map[string][]string{}
	for _, t := range targets {
		b := e.targetBase(t)
		if _, ok := groups[b]; !ok {
			order = append(order, b)
		}
		groups[b] = append(groups[b], t)
	}
	var out []Merged
	remaining := e.cap()
	for i, b := range order {
		ts := groups[b]
		if e.TrainFill.Enabled() {
			var filling []string
			for _, target := range ts {
				if e.FillBypass != nil && e.FillBypass(target) {
					out = append(out, e.land(ctx, r, []string{target}, b)...)
					remaining--
					if remaining == 0 {
						e.holdUnreached(r, groups, order[i+1:], ts[slices.Index(ts, target)+1:])
						return out
					}
				} else {
					filling = append(filling, target)
				}
			}
			ts = filling
			if len(ts) == 0 {
				continue
			}
			cands := make([]roundcfg.FillCandidate, len(ts))
			for i, t := range ts {
				cands[i].ID = t
				if e.ReadySince != nil {
					cands[i].Ready = e.ReadySince(t)
				}
			}
			now := time.Now()
			if e.Now != nil {
				now = e.Now()
			}
			d := e.TrainFill.Decide(b, cands, remaining, now)
			if len(d.Land) == 0 {
				key := fmt.Sprintf("%s:%d/%d", b, len(ts), e.TrainFill.Max)
				r.FillStatus = append(r.FillStatus, d.Holding)
				r.Filling[b] = key
				if e.Filling[b] != key {
					r.Holding = append(r.Holding, d.Holding)
				}
				continue
			}
			ts = d.Land
		}
		out = append(out, e.land(ctx, r, ts, b)...)
		remaining -= len(ts)
		if remaining == 0 {
			e.holdUnreached(r, groups, order[i+1:], nil)
			break
		}
	}
	return out
}

// holdUnreached names the cap as the reason for the targets the merge step ran
// out of room before: the rest of the current group, then every later base.
func (e Env) holdUnreached(r *Result, groups map[string][]string, bases, rest []string) {
	for _, t := range rest {
		r.hold(t, capHold(e.cap()))
	}
	for _, b := range bases {
		for _, t := range groups[b] {
			r.hold(t, capHold(e.cap()))
		}
	}
}

func (e Env) gate(ctx context.Context, target, base string) Merged {
	g := e.gateTarget(ctx, target, base)
	if g.Landed {
		return Merged{Target: target, Landed: true, Detail: "into " + base}
	}
	v := g.Verdict
	if v == "" {
		v = "gate-error"
	}
	return Merged{Target: target, Verdict: v, Hold: worker.ClassifyVerdict(v).Hold, Detail: g.Detail}
}

func excludedIdx(cs []worker.TrainCulprit, target string) int {
	return slices.IndexFunc(cs, func(c worker.TrainCulprit) bool { return c.Target == target })
}

func (e Env) train(ctx context.Context, targets []string, base string) []Merged {
	tr := e.trainTargets(ctx, targets, base)
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
		case excludedIdx(tr.Excluded, t) >= 0:
			c := tr.Excluded[excludedIdx(tr.Excluded, t)]
			m.Verdict, m.Hold, m.Detail = c.Gate.Verdict, worker.ClassifyVerdict(c.Gate.Verdict).Hold, strutil.FirstLine(c.Gate.Err)
		case slices.Contains(tr.Landed, t):
			m.Landed, m.Detail = true, "train into "+base
		case v == "base-moved":
			m.Skip, m.Detail = true, "base moved under the train, nothing landed"
		case tr.Unreproduced:
			m.Skip, m.Detail = true, "train red did not reproduce (flake); will retrain"
		case tr.Culprit != "" && t != tr.Culprit && !strings.Contains(tr.Culprit, t):
			m.Skip, m.Detail = true, "train broke on "+tr.Culprit+"; retried without it"
		case len(tr.Interaction) > 0 && !slices.Contains(tr.Interaction, t):
			m.Skip, m.Detail = true, "bystander of an interaction among "+strings.Join(tr.Interaction, ", ")+"; retried without it"
		case len(tr.Interaction) > 0:
			m.Verdict, m.Hold = v, worker.ClassifyVerdict(v).Hold
			m.Detail = "fix on one of: " + strings.Join(tr.Interaction, ", ") + "; bounce neither"
		default:
			m.Verdict, m.Hold, m.Detail = v, worker.ClassifyVerdict(v).Hold, tr.Detail
		}
		out = append(out, m)
	}
	return out
}

// review mints the due architecture reviews. A failed mint is the
// orchestrator's to look at, not a reason to stop the tick's other steps.
func (e Env) review(ctx context.Context, r *Result) ([]string, error) {
	if e.Review == nil {
		return nil, nil
	}
	a, ready, err := e.Review.Facts(ctx)
	var ids []string
	if err == nil {
		if a.Count < 0 && e.Observe {
			r.Unknown = append(r.Unknown, Item{Kind: "architecture-review", Target: "round", Why: a.Unavailable})
		}
		if a = round.DecideReview(a, ready); a.Due {
			r.propose(Action{Action: "mint", Target: strings.Join(a.Areas, ","), Detail: "architecture review: " + a.Trigger}, "item creation and minted IDs")
			if e.Observe {
				r.Unknown = append(r.Unknown, Item{Kind: "assign", Target: "minted reviews", Why: "new review IDs and priority assignments depend on successful minting"})
			} else {
				ids, err = e.Review.Mint(ctx, a)
			}
		}
	}
	if err != nil {
		r.NeedsYou = append(r.NeedsYou, Item{Kind: "architecture-review", Target: "mint", Why: strutil.FirstNonEmpty(strutil.FirstLine(err.Error()), "the review could not be minted")})
	}
	for _, id := range ids {
		e.audit(r, Action{Action: "mint", Target: id, Detail: "architecture review"})
	}
	return ids, nil
}

// assign hands ready candidates to idle active slots, within the cap less the
// follow-ups this tick already started, and never onto a slot one took
// (observation leaves the registry as it was, so Slots still shows it idle).
func (e Env) assign(ctx context.Context, r *Result, minted []string, started int, taken []string) error {
	idle := 0
	for _, s := range e.Slots() {
		if e.Active != nil && !slices.Contains(e.Active(), s.Name) || slices.Contains(taken, s.Name) {
			continue
		}
		if worker.IsIdle(s.State) || s.State == "" {
			if s.Issue == "" {
				idle++
			}
		}
	}
	n := idle
	if c := e.cap() - started; n > c {
		n = c
	}
	if n <= 0 {
		return nil
	}
	// The minted reviews go first: they are the reason the tick minted them.
	// One that is refused stays a candidate for a later tick.
	queue := make([]string, 0, len(minted))
	queue = append(queue, minted...)
	cands, err := e.Candidates(ctx)
	if err != nil {
		return err
	}
	for _, c := range cands {
		if !c.Ready() || slices.Contains(minted, c.ID) {
			if e.Observe && !c.Ready() {
				r.NeedsYou = append(r.NeedsYou, Item{Kind: "candidate-hold", Target: c.ID, Why: "candidate is not ready: " + candidateHold(c)})
			}
			continue
		}
		// An epic or an unplanned Major item waits for a person (#2570).
		if c.AutoHold != "" {
			r.NeedsYou = append(r.NeedsYou, Item{Kind: "autopilot-hold", Target: c.ID, Why: c.AutoHold})
			continue
		}
		queue = append(queue, c.ID)
	}
	for _, id := range queue {
		if n == 0 {
			break
		}
		r.propose(Action{Action: "assign", Target: id, Detail: "ready candidate; active idle slot available"}, "claim, slot selection, branch creation and dispatch preflight")
		if e.Observe {
			n--
			continue
		}
		agents, err := e.Assign(ctx, id)
		switch {
		case err == nil:
			e.audit(r, Action{Action: "assign", Target: id, Detail: "to " + strings.Join(agents, " and ")})
			n--
		case isRefusal(err):
			// Not ready after all, or every slot filled meanwhile: try the next.
			// A quota refusal is the cap showing through a pool the cap check
			// left open (Claude workers beside Codex logins): say why.
			var blk *round.BlockedError
			if errors.As(err, &blk) && blk.By == blockedby.Quota && r.Capped == "" {
				r.Capped = blk.Msg
			}
			// A gate or train holds the land lock for the slot assign picked (or
			// for slots it cannot name): later candidates pick the same slot, so
			// defer them to the next tick.
			if errors.As(err, &blk) && blk.By == worker.GateRunning {
				r.Deferred = "gate running, deferred: " + blk.Msg
				return nil
			}
			// The batch filled during this tick: no later candidate fits either.
			if errors.As(err, &blk) && blk.By == blockedby.Batch {
				r.BatchFull = true
				return nil
			}
		default:
			return fmt.Errorf("assign %s: %w", id, err)
		}
	}
	return nil
}

// isRefusal reports whether err is Assign declining without anything having
// gone wrong: a *round.BlockedError other than "no round", which means the
// autopilot has no round to work for.
func isRefusal(err error) bool {
	var blk *round.BlockedError
	return errors.As(err, &blk) && blk.By != blockedby.NoRound
}
