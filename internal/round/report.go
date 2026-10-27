package round

import (
	"context"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/stamp"
	"github.com/l4ci/rota/internal/strutil"
	"regexp"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/worker"
)

// ReportStates are the states `round report` accepts (C8). busy is not one:
// only assign sets it, when it hands the slot a brief.
var ReportStates = []string{"done", "blocked", "idle", "dead", "limited"}

var rePRNumberArg = regexp.MustCompile(`^#?\d+$`)

// ReportOpts are the flags of `rota round report`.
type ReportOpts struct {
	Slot, State, Evidence, PR string
	// Attempt is the attempt id the worker was briefed with, "" for none.
	Attempt string
	// Issues is a review item's done evidence in place of a PR: `#139,#140` or `none`.
	Issues string
	// Event is the worker event kind this report records (worker.EventBlocked
	// and the others), "" for a plain `round report`. An event is the worker's
	// own word on its own slot, so it is written on any host; a plain report
	// stands in for a pane poll and stays solo-only. Body is the event's text.
	Event, Body string
}

// Reported is what ReportSlot did. Evidence is echoed, never stored.
type Reported struct {
	NoFindings                          bool
	Slot, State, Previous, PR, Evidence string
	Issues                              []string
	Changed                             bool
}

// reportKinds maps the reported states that are ledger events to their kind.
var reportKinds = map[string]string{"done": ledger.KindDone, "blocked": ledger.KindBlocked, "limited": ledger.KindLimited}

// ReportSlot records what a worker's result said, writing the fields a pane
// poll writes: state (lowercase) and pr, plus the slot's event when o.Event is
// set. A plain report is a second writer beside a host's poll and would race
// it, so only a solo round takes one; an event is read ahead of the pane
// (worker.Decide), so it is written on every host. Re-reporting the same state
// and PR changes nothing.
func (e Env) ReportSlot(ctx context.Context, root string, o ReportOpts) (Reported, error) {
	res := Reported{Slot: o.Slot, Evidence: o.Evidence}
	state := strings.ToLower(strings.TrimSpace(o.State))
	if o.Event != "" {
		spec, ok := worker.EventSpecFor(o.Event)
		if !ok {
			return res, usage("event must be one of %s, got %q", strings.Join(worker.EventKinds, ", "), o.Event)
		}
		state = spec.State
	} else if !slices.Contains(ReportStates, state) {
		return res, usage("--state must be one of %s", strings.Join(ReportStates, ", "))
	}
	res.State = state
	pr := strings.TrimSpace(o.PR)
	if pr != "" && !worker.IsPRURL(pr) && !rePRNumberArg.MatchString(pr) {
		return res, usage("--pr must be a PR or MR URL or a number, got %q", o.PR)
	}
	res.PR = pr
	var result worker.ReviewResult
	if iss := strings.TrimSpace(o.Issues); iss != "" {
		if pr != "" {
			return res, usage("--issues and --pr are exclusive: a review item reports issues, any other item a PR")
		}
		result = worker.ParseReviewResult("issues:" + iss)
		if !result.Complete() {
			return res, usage("--issues must be issue numbers or keys like #139,PROJ-140, or none for no findings, got %q", o.Issues)
		}
		res.Issues = result.Issues()
		res.NoFindings = result.NoFindings()
	}
	switch h := worker.RegistryHost(root); {
	case worker.IsSoloHost(h) || o.Event != "":
	case h == "":
		return res, &exitcode.Error{Exit: exitcode.ExitUsage, Message: "no round host is recorded: run rota round start first",
			Hint: "round report is for solo rounds; under herdr or tmux, rota worker poll records the state"}
	default:
		return res, &exitcode.Error{Exit: exitcode.ExitUsage, Message: fmt.Sprintf("the round host is %s, not solo: the pane is the truth", h),
			Hint: "rota worker poll records a pane's state; round report would race it"}
	}
	found, moved := false, false
	var fenced error
	seq := 0
	var stateErr error
	var held ledger.Entry
	err := worker.Update(root, func(doc *worker.Doc) {
		s := doc.Slot(o.Slot)
		if s == nil {
			return
		}
		found = true
		if fenced = worker.FenceAttempt(s, o.Attempt); fenced != nil {
			return
		}
		res.Previous = s.State()
		held = ledger.Entry{Issue: worker.HeldID(s.Task(), s.Branch(), s.Name()), Slot: s.Name(), Account: s.Account(), Harness: s.Kind(), PR: s.PR()}
		stamped := ""
		if o.Event != "" {
			stamped = stamp.Format(e.now())
			res.Changed = true
			if o.Event == worker.EventHeartbeat { // alive, not news, no event and no state of its own
				s.Heartbeat(stamped)
				res.State = res.Previous
				return
			}
			s.SetEvent(o.Event, strings.TrimSpace(o.Body), stamped)
			seq = s.Event().Seq
			if state == "" { // an event that is no state change (a review reply)
				res.State = res.Previous
				return
			}
		}
		if res.Previous != state {
			moved = true
			if stateErr = s.MarkState(state, stamped); stateErr != nil {
				return
			}
			res.Changed = true
			// A reply on a PR has no state, so it never reaches this edge: the
			// review cursor stays where the bounce left it.
			if worker.IsDone(state) {
				s.BaselineReview(e.now())
			}
		}
		s.ClearSeen() // a report is news to `round wait`, even of the same state
		if pr != "" && s.PR() != pr {
			s.SetPR(pr)
			res.Changed = true
		}
		if result.Complete() && !s.ReviewResult().Equal(result) {
			s.SetReviewResult(result)
			res.Changed = true
		}
	})
	if err != nil {
		return res, err
	}
	if stateErr != nil {
		return res, stateErr
	}
	if fenced != nil {
		return res, fenced
	}
	if !found {
		return res, &exitcode.Error{Exit: exitcode.ExitResolution, Message: fmt.Sprintf("slot '%s' is not in the pool", o.Slot)}
	}
	kind := reportKinds[state]
	if o.Event != "" {
		spec, _ := worker.EventSpecFor(o.Event)
		kind = spec.Ledger
		if spec.EveryRow {
			// Every such event is a row, not only the edge into its state.
			moved = true
			held.Detail = ledger.Detail("seq", seq)
			if spec.RowBody {
				held.Detail = ledger.Detail("event", o.Event, "body", o.Body, "seq", seq)
			}
		}
	}
	if kind != "" && moved {
		held.Kind, held.PR = kind, strutil.FirstNonEmpty(pr, held.PR)
		if kind == ledger.KindDone {
			held.Detail = ledger.Detail("headroom", worker.LedgerHeadroom(ctx, e.Worker.Accounts(), root, held.Harness, held.Account))
		}
		if err := e.Worker.LedgerAppendErr(root, held); err != nil {
			return res, fmt.Errorf("slot %s is now %s, but the round ledger row was not written: %w", o.Slot, state, err)
		}
	}
	return res, nil
}
