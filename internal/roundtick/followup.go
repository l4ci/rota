package roundtick

import (
	"context"
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/landing"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/worker"
)

// FollowUp is a queued PR a person asked to continue: its worker moved on and
// review input from a human writer waits on it (round.FollowUps). Who names
// the people, for the report. Item is the listed follow-up itself, which Start
// receives so it needs no state shared with List.
type FollowUp struct {
	Issue, PR, Who string
	Item           round.FollowUp
}

// FollowUpSources are the seams of the follow-up step (#2580). A nil List
// means the round has none.
type FollowUpSources struct {
	// List is the follow-ups waiting, oldest human input first.
	List func(ctx context.Context) ([]FollowUp, error)
	// Auto is round.reviewLoop auto: only then does the autopilot hand review
	// input to a worker, so only then does it start a follow-up.
	Auto bool
	// Reserve names the follow-up reserve slots (round.followupReserve): the
	// roster names right after the active ones. Size is the configured count;
	// fewer names means the roster is too short.
	Reserve func() []string
	Size    int
	// Receives is Transfer's rule for its receiving slot (round.Env.Receives):
	// an error says why slot would be refused. Nil refuses nothing.
	Receives func(ctx context.Context, slot string, reserve bool) error
	// Start transfers the follow-up's issue to slot with its review input as
	// the brief; reserve is true for a reserve slot. A *round.BlockedError is
	// a refusal that leaves the follow-up pending.
	Start func(ctx context.Context, f FollowUp, slot string, reserve bool) error
	// StartFailed transfers the issue of the queued PR pr, whose head failed
	// the gate, to slot with the gate's verdict (why) as the brief. No bounce
	// is counted. A *round.BlockedError is a refusal; nil means the round
	// routes no failed gate to a worker.
	StartFailed func(ctx context.Context, pr, why, slot string, reserve bool) error
}

// FollowUpPorts are the outside reads and writes the follow-up sources are
// built from: the registry, the forge and the transfer verb, each behind one
// function.
type FollowUpPorts struct {
	Settings roundcfg.Settings
	// Registry is the tolerant registry read.
	Registry func() worker.Registry
	// Forge opens the review forge; it is asked only when a PR is queued.
	Forge func(ctx context.Context) (round.ReviewForge, error)
	// Receives says why a transfer would refuse slot (round.Env.Receives).
	Receives func(ctx context.Context, slot string, reserve bool) error
	// List reads the follow-ups through the forge, with the warnings it raised.
	List func(ctx context.Context, f round.ReviewForge) ([]round.FollowUp, []string, error)
	// Start continues one follow-up in slot and returns the warnings it raised.
	Start func(ctx context.Context, f round.FollowUp, slot string, reserve bool) ([]string, error)
	// StartFailed continues the queued PR pr, whose head failed the gate with
	// why, in slot (round.ContinueFailed) and returns the warnings it raised.
	StartFailed func(ctx context.Context, pr, why, slot string, reserve bool) ([]string, error)
	Warn        func(string)
}

// NewFollowUpSources assembles the follow-up step (#2580) from its ports. A
// round with no queued PR asks the forge nothing; the people who asked are
// joined into Who; the reserve slots follow the settings.
func NewFollowUpSources(p FollowUpPorts) FollowUpSources {
	s := FollowUpSources{
		Auto:     p.Settings.ReviewLoop == roundcfg.ReviewLoopAuto,
		Size:     p.Settings.FollowupReserve,
		Reserve:  func() []string { return round.Reserve(p.Registry(), p.Settings) },
		Receives: p.Receives,
		List: func(ctx context.Context) ([]FollowUp, error) {
			if len(p.Registry().PRs()) == 0 {
				return nil, nil
			}
			fg, err := p.Forge(ctx)
			if err != nil {
				return nil, err
			}
			list, warns, err := p.List(ctx, fg)
			for _, warn := range warns {
				p.Warn(warn)
			}
			var out []FollowUp
			for _, u := range list {
				out = append(out, FollowUp{Issue: u.Issue, PR: u.PR, Who: strings.Join(u.Humans, ", "), Item: u})
			}
			return out, err
		},
		Start: func(ctx context.Context, f FollowUp, slot string, reserve bool) error {
			warns, err := p.Start(ctx, f.Item, slot, reserve)
			for _, warn := range warns {
				p.Warn(warn)
			}
			return err
		},
	}
	if p.StartFailed != nil {
		s.StartFailed = func(ctx context.Context, pr, why, slot string, reserve bool) error {
			warns, err := p.StartFailed(ctx, pr, why, slot, reserve)
			for _, warn := range warns {
				p.Warn(warn)
			}
			return err
		}
	}
	return s
}

// followUps starts the waiting follow-ups before any backlog work and returns
// how much of the cap the tick has used, which the assign step counts: used
// (assign-ahead) plus the follow-ups it started. Each takes the first
// idle active slot, else the first free reserve slot. One that cannot start
// stays pending (nothing it consumed moved) and is reported as a followup item
// naming what it lacks. A support machine starts none: the queued PRs are main's.
// Under observation each start is a proposal and Start is never called. taken
// names the slots assign-ahead and the follow-ups took (or would take), which
// assign skips.
func (e Env) followUps(ctx context.Context, r *Result, used int, took []string) (started int, taken []string, err error) {
	started, taken = used, slices.Clone(took)
	if started, taken, err = e.askedFollowUps(ctx, r, started, taken); err != nil || r.Deferred != "" {
		return started, taken, err
	}
	return e.failedFollowUps(ctx, r, started, taken)
}

// askedFollowUps starts the follow-ups people asked for (#2580).
func (e Env) askedFollowUps(ctx context.Context, r *Result, started int, taken []string) (int, []string, error) {
	src := e.FollowUp
	if src.List == nil || src.Start == nil || e.Role == roundcfg.RoleSupport {
		return started, taken, nil
	}
	list, err := src.List(ctx)
	if err != nil {
		r.NeedsYou = append(r.NeedsYou, Item{Kind: "followup", Target: "poll", Why: "the follow-up poll failed: " + strutil.FirstLine(err.Error())})
		return started, taken, nil
	}
	pending := func(f FollowUp, why string) {
		r.NeedsYou = append(r.NeedsYou, Item{Kind: "followup", Target: f.Issue, Why: fmt.Sprintf("%s on %s waits: %s", strutil.FirstNonEmpty(f.Who, "review input"), f.PR, why)})
	}
	for _, f := range list {
		switch {
		case !src.Auto:
			pending(f, "round.reviewLoop is manual; run `rota round transfer "+f.Issue+" --to <free round slot> --body-file <review>`; reserve slots take follow-ups from the auto tick only")
			continue
		case r.Capped != "":
			pending(f, r.Capped)
			continue
		case started >= e.cap():
			pending(f, fmt.Sprintf("this tick already used %d of round.autopilotCap", started))
			continue
		}
		slot, reserve, why := e.followUpSlot(ctx, taken)
		if slot == "" {
			pending(f, why)
			continue
		}
		detail := "to " + slot
		if reserve {
			detail += " (reserve)"
		}
		detail += ", asked by " + strutil.FirstNonEmpty(f.Who, "a person") + " on " + f.PR
		r.propose(Action{Action: "followup", Target: f.Issue, Detail: detail}, "bounce cap, claim, overlap, account preflight and dispatch")
		if e.Observe {
			started++
			taken = append(taken, slot)
			continue
		}
		err := src.Start(ctx, f, slot, reserve)
		switch {
		case err == nil:
			e.audit(r, Action{Action: "followup", Target: f.Issue, Detail: detail})
			started++
			taken = append(taken, slot)
		case !isRefusal(err) && errors.As(err, new(*round.BlockedError)):
			return started, taken, fmt.Errorf("follow-up %s: %w", f.Issue, err) // no round to work for
		default:
			// Any other failure leaves the follow-up pending: nothing it consumed
			// moved, and the next tick tries again.
			pending(f, "the transfer to "+slot+" did not start: "+strutil.FirstLine(err.Error()))
			// A gate or train holds the land lock: every later start would wait on it too.
			var blk *round.BlockedError
			if errors.As(err, &blk) && blk.By == landing.GateRunning {
				r.Deferred = "gate running, deferred: " + blk.Msg
				return started, taken, nil
			}
		}
	}
	return started, taken, nil
}

// followUpSlot picks a follow-up's receiver: the first idle active slot, else
// the first reserve slot that is idle or done with its PR (the transfer queues
// that PR). Either must also pass the transfer's receiver rule (Receives). why
// names the limiting resource when there is none.
func (e Env) followUpSlot(ctx context.Context, taken []string) (slot string, reserve bool, why string) {
	receives := func(name string, reserve bool) error {
		if e.FollowUp.Receives == nil {
			return nil
		}
		return e.FollowUp.Receives(ctx, name, reserve)
	}
	slots := e.Slots()
	var active []string
	if e.Active != nil {
		active = e.Active()
	}
	for _, s := range slots {
		if !slices.Contains(taken, s.Name) && (active == nil || slices.Contains(active, s.Name)) && (worker.IsIdle(s.State) || s.State == "") && s.Issue == "" && receives(s.Name, false) == nil {
			return s.Name, false, ""
		}
	}
	src := e.FollowUp
	if src.Size <= 0 {
		return "", false, "no active slot is idle and round.followupReserve is 0"
	}
	var names []string
	if src.Reserve != nil {
		names = src.Reserve()
	}
	if len(names) == 0 {
		return "", false, fmt.Sprintf("no active slot is idle and round.followupReserve is %d but the roster has no name past the active slots", src.Size)
	}
	var busy []string
	for _, name := range names {
		i := slices.IndexFunc(slots, func(s Slot) bool { return s.Name == name })
		if slices.Contains(taken, name) {
			busy = append(busy, name+" taken this tick")
			continue
		}
		if i < 0 {
			busy = append(busy, name+" not provisioned (run `rota round start`)")
			continue
		}
		s := slots[i]
		if (worker.IsIdle(s.State) || s.State == "") && s.Issue == "" || worker.IsDone(s.State) && s.PR != "" {
			err := receives(name, true)
			if err == nil {
				return name, true, ""
			}
			busy = append(busy, name+" refused: "+strutil.FirstLine(err.Error()))
			continue
		}
		busy = append(busy, name+" "+strutil.FirstNonEmpty(s.State, "busy"))
	}
	why = "no active slot is idle and every reserve slot is busy (" + strings.Join(busy, ", ") + ")"
	if len(names) < src.Size {
		why += fmt.Sprintf("; the roster holds %d of the %d reserve slots round.followupReserve asks for", len(names), src.Size)
	}
	return "", false, why
}

// routedVerdict is a gate verdict the follow-up step hands back to a worker
// (#4247): one about the PR's content that a push fixes. An interaction is the
// orchestrator's to fix, and an infrastructure refusal clears without a push.
func routedVerdict(v string) bool {
	switch v {
	case landing.GateStale, landing.GateProvenanceFail, landing.GateVerifyFailed, landing.GateVacuousTests:
		return true
	}
	return false
}

// failedFollowUps (#4247) starts a follow-up for each queued PR whose current
// head failed the gate with a routed verdict, this tick or an earlier one: its
// worker moved on (assign-ahead or a manual assign), so nobody else would fix
// it. Only under round.reviewLoop auto; otherwise, or for an interaction, it
// stays under "needs you" as before. It shares the cap and the slots with the
// follow-ups people asked for. A head that moved since the gate starts nothing:
// the new head gets its own gate. One that cannot start stays reported and its
// failure kept, so a later tick tries again.
func (e Env) failedFollowUps(ctx context.Context, r *Result, started int, taken []string) (int, []string, error) {
	src := e.FollowUp
	if src.StartFailed == nil || !src.Auto || e.Head == nil || e.Queued == nil || e.Role == roundcfg.RoleSupport {
		return started, taken, nil
	}
	if len(r.gateFailed) == 0 && len(r.FailedHeads) == 0 {
		return started, taken, nil
	}
	// The gate ran for minutes under this tick's read cache: a push made
	// meanwhile must show, so the head is read past it.
	readHead := e.Head
	if e.FreshHead != nil {
		readHead = e.FreshHead
	}
	for _, ref := range e.Queued() {
		head, err := readHead(ctx, ref)
		if err != nil || head == "" {
			continue
		}
		why := failedWhy(r, ref, head)
		if v, _, _ := strings.Cut(why, ":"); !routedVerdict(v) {
			continue
		}
		pending := func(reason string) {
			r.NeedsYou = append(r.NeedsYou, Item{Kind: "followup", Target: ref, Why: "the gate failure on " + ref + " waits: " + reason})
		}
		switch {
		case r.Capped != "":
			pending(r.Capped)
			continue
		case started >= e.cap():
			pending(fmt.Sprintf("this tick already used %d of round.autopilotCap", started))
			continue
		}
		slot, reserve, whyNot := e.followUpSlot(ctx, taken)
		if slot == "" {
			pending(whyNot)
			continue
		}
		detail := "to " + slot
		if reserve {
			detail += " (reserve)"
		}
		detail += ", gate failed: " + why
		r.propose(Action{Action: "followup", Target: ref, Detail: detail}, "queued record, claim, overlap, account preflight and dispatch")
		if e.Observe {
			started++
			taken = append(taken, slot)
			continue
		}
		err = src.StartFailed(ctx, ref, why, slot, reserve)
		switch {
		case err == nil:
			e.audit(r, Action{Action: "followup", Target: ref, Detail: detail})
			started++
			taken = append(taken, slot)
			// A worker has it now: it no longer waits for a person.
			r.NeedsYou = slices.DeleteFunc(r.NeedsYou, func(it Item) bool { return it.Kind == "gate-failed" && it.Target == ref })
		case !isRefusal(err) && errors.As(err, new(*round.BlockedError)):
			return started, taken, fmt.Errorf("follow-up %s: %w", ref, err) // no round to work for
		default:
			pending("the transfer to " + slot + " did not start: " + strutil.FirstLine(err.Error()))
			var blk *round.BlockedError
			if errors.As(err, &blk) && blk.By == landing.GateRunning {
				r.Deferred = "gate running, deferred: " + blk.Msg
				return started, taken, nil
			}
		}
	}
	return started, taken, nil
}

// failedWhy is the failure recorded for ref at head: this tick's verdict
// first, else one carried at the same head (a failed head follows its PR
// whatever target name judged it).
func failedWhy(r *Result, ref, head string) string {
	if f, ok := r.gateFailed[ref]; ok {
		if f.Head == head {
			return f.Why
		}
		return ""
	}
	if f, ok := r.FailedHeads[ref]; ok {
		if f.Head == head {
			return f.Why
		}
		return ""
	}
	for _, f := range r.FailedHeads {
		if f.Head == head {
			return f.Why
		}
	}
	return ""
}
