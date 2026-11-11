package roundtick

import (
	"context"
	"errors"
	"fmt"
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
	// Start transfers the follow-up's issue to slot with its review input as
	// the brief; reserve is true for a reserve slot. A *round.BlockedError is
	// a refusal that leaves the follow-up pending.
	Start func(ctx context.Context, f FollowUp, slot string, reserve bool) error
}

// followUps starts the waiting follow-ups before any backlog work and returns
// how many it started, which the assign step's cap counts. Each takes the first
// idle active slot, else the first free reserve slot. One that cannot start
// stays pending (nothing it consumed moved) and is reported as a followup item
// naming what it lacks. A support machine starts none: the queued PRs are main's.
// Under observation each start is a proposal and Start is never called. taken
// names the slots the follow-ups took (or would take), which assign skips.
func (e Env) followUps(ctx context.Context, r *Result) (started int, taken []string, err error) {
	src := e.FollowUp
	if src.List == nil || src.Start == nil || e.Role == roundcfg.RoleSupport {
		return 0, nil, nil
	}
	list, err := src.List(ctx)
	if err != nil {
		r.NeedsYou = append(r.NeedsYou, Item{Kind: "followup", Target: "poll", Why: "the follow-up poll failed: " + strutil.FirstLine(err.Error())})
		return 0, nil, nil
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
			pending(f, fmt.Sprintf("this tick already started %d (round.autopilotCap)", started))
			continue
		}
		slot, reserve, why := e.followUpSlot(taken)
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
			if errors.As(err, &blk) && blk.By == worker.GateRunning {
				r.Deferred = "gate running, deferred: " + blk.Msg
				return started, taken, nil
			}
		}
	}
	return started, taken, nil
}

// followUpSlot picks a follow-up's receiver: the first idle active slot, else
// the first reserve slot that is idle or done with its PR (the transfer queues
// that PR). why names the limiting resource when there is none.
func (e Env) followUpSlot(taken []string) (slot string, reserve bool, why string) {
	slots := e.Slots()
	var active []string
	if e.Active != nil {
		active = e.Active()
	}
	for _, s := range slots {
		if !slices.Contains(taken, s.Name) && (active == nil || slices.Contains(active, s.Name)) && (worker.IsIdle(s.State) || s.State == "") && s.Issue == "" {
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
			return name, true, ""
		}
		busy = append(busy, name+" "+strutil.FirstNonEmpty(s.State, "busy"))
	}
	why = "no active slot is idle and every reserve slot is busy (" + strings.Join(busy, ", ") + ")"
	if len(names) < src.Size {
		why += fmt.Sprintf("; the roster holds %d of the %d reserve slots round.followupReserve asks for", len(names), src.Size)
	}
	return "", false, why
}
