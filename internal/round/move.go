package round

import (
	"context"
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/escalation"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/roundlease"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// C10 (#76): moving an issue that is already assigned. The tracker changes
// first and the registry second, so a crash between them leaves a
// claim-mismatch that reconcile reports; every step is skipped when already
// done, so a repeated call finishes the rest.

// Blocked reasons of return, transfer and reclaim (exit 4, `blockedBy`).
const (
	BlockNotYourSlot = "not your slot"
	BlockSameSlot    = "same slot"
	BlockHealthy     = "healthy"
	BlockLiveAgent   = "live agent"
)

// Slot health, as reclaim reports it.
const (
	HealthDead    = "dead"
	HealthStalled = "stalled"
	HealthHealthy = "healthy"
	HealthIdle    = "idle"
)

// HumanTarget is the `--to` value that hands an issue to the human.
const HumanTarget = "human"

// wrap maps what a step failed with onto the exit table: a missing item is 3,
// a busy registry lock is 6, git, the host and the tracker are 5.
func wrap(err error) error {
	if err == nil {
		return nil
	}
	var we *exitcode.Error
	var blk *BlockedError
	switch {
	case errors.As(err, &we), errors.As(err, &blk):
		return err
	case errors.Is(err, backlog.ErrNotFound):
		return &exitcode.Error{Exit: exitcode.ExitResolution, Message: err.Error()}
	case errors.Is(err, fsio.ErrLockTimeout):
		return &exitcode.Error{Exit: exitcode.ExitRetry, Message: err.Error()}
	}
	return &exitcode.Error{Exit: exitcode.ExitUnavailable, Message: err.Error()}
}

func registryRound(root string) int {
	n, _ := worker.LoadRegistry(root).Round()
	return n
}

// holdsLease: this process is the orchestrator the lease names.
func (e Env) holdsLease(ctx context.Context, root string, pid int, getenv func(string) string) (bool, error) {
	cd, err := e.commonDir(ctx, root)
	if err != nil {
		return false, err
	}
	le := e.leaseEnv()
	lease, st, err := le.Read(cd)
	if err != nil {
		return false, err
	}
	holder := le.Discover(pid, getenv)
	return (st == roundlease.Live || st == roundlease.Foreign) && holder.SameAs(lease, le.Host), nil
}

// slotIssue is the issue a slot holds, in the backend's spelling.
func slotIssue(s *worker.Slot) string {
	return heldID(s.Task(), s.Branch(), s.Name())
}

// freeSlot records a slot as idle and parked: no issue, no claim, no PR.
func freeSlot(root, name string, clearHandle bool) error {
	return editSlot(root, name, func(s *worker.Slot) error { s.Park(clearHandle); return nil })
}

// handoff is the stand-in for D1's note: one comment on the issue, ending with
// a marker C4's answer rule never reads as an answer.
type handoff struct {
	verb, from string
	round      int
	branch     string
	head       string
	salvaged   bool
	reason     string
	note       string
}

func (h handoff) marker() string { return marker.Handoff(h.from, h.round) }

func (h handoff) body() string {
	state := "committed"
	if h.salvaged {
		state += ", salvage commit"
	}
	head := h.head
	if head == "" {
		head = "(none)"
	}
	note := strings.TrimSpace(h.note)
	if note == "" {
		note = "(no note)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**rota handoff** (%s, from %s)\n\n", h.verb, h.from)
	fmt.Fprintf(&b, "Branch: `%s`\nHead: %s\nState: %s\nReason: %s\n\n", h.branch, head, state, strings.TrimSpace(h.reason))
	fmt.Fprintf(&b, "Done and next:\n%s\n\n%s", note, h.marker())
	return b.String()
}

// post adds the handoff comment unless the same one is already there. Comments
// are posted as kind `feedback`, the kind assign uses for its own notes.
func (h handoff) post(be Board, id string) (commentID string, posted bool, err error) {
	body := h.body()
	if cs, err := be.Comments(id, "feedback"); err == nil {
		for _, c := range cs {
			if strings.TrimSpace(c.Text) == strings.TrimSpace(body) {
				return "", false, nil
			}
		}
	}
	commentID, err = be.AddComment(id, "feedback", body)
	return commentID, err == nil, err
}

var reHandoffBranch = regexp.MustCompile("(?m)^Branch: `([^`]+)`$")

// latestHandoffBranch is the branch named by the newest rota:handoff (or legacy
// hv:handoff) comment on the issue, "" when there is none.
func latestHandoffBranch(be Board, id string) string {
	cs, err := be.Comments(id, "feedback")
	if err != nil {
		return ""
	}
	for i := len(cs) - 1; i >= 0; i-- {
		if t := cs[i].Text; marker.HasHandoff(t) {
			if m := reHandoffBranch.FindStringSubmatch(cs[i].Text); m != nil {
				return m[1]
			}
			return ""
		}
	}
	return ""
}

// releaseClaims gives the slot's claim back: the registry's claimId when
// known, and with sweep every open claim whose id starts `<slot>@` (the
// registry may have lost it). A claim already gone is not an error.
func releaseClaims(be Board, id, slot, claimID string, sweep bool) (bool, error) {
	released := false
	if claimID != "" {
		ok, err := be.Release(id, claimID)
		if err != nil {
			return released, err
		}
		released = ok
	}
	for i := 0; sweep && i < 20; i++ {
		c := openClaimWith(be, id, slot+"@")
		if c == "" {
			break
		}
		ok, err := be.Release(id, c)
		if err != nil || !ok {
			return released, err
		}
		released = true
	}
	return released, nil
}

// ---- return -----------------------------------------------------------------

// ReturnOpts are the flags of `rota round return`.
type ReturnOpts struct {
	Slot, Reason string
	Note         string // the text of --note-file
	// InSlot: the caller's working directory is inside the slot's worktree, so
	// the caller is the slot's own worker. Otherwise it must hold the lease.
	InSlot    bool
	HolderPID int
	Getenv    func(string) string
}

// Returned is what Return did.
type Returned struct {
	Slot, Issue, Branch, Head string
	Salvaged, Released        bool
	CommentID                 string
	Changed                   bool
	Warnings                  []string
}

// Return is the worker's verb: it parks the slot (the branch is pushed and the
// worktree switched off it), posts the handoff comment, releases the claim,
// clears the in-progress state and frees the slot. The branch stays and no PR
// is closed.
func (e Env) Return(ctx context.Context, root string, be Board, o ReturnOpts) (res Returned, err error) {
	if strings.TrimSpace(o.Reason) == "" {
		return res, usage("--reason is required")
	}
	s := worker.LoadRegistry(root).Slot(o.Slot)
	if s == nil {
		return res, &exitcode.Error{Exit: exitcode.ExitResolution, Message: fmt.Sprintf("slot %s is not in the pool", o.Slot)}
	}
	id := slotIssue(s)
	if id == "" {
		return res, &exitcode.Error{Exit: exitcode.ExitResolution, Message: fmt.Sprintf("slot %s holds no issue", o.Slot)}
	}
	res.Slot = o.Slot
	if !o.InSlot {
		ok, err := e.holdsLease(ctx, root, o.HolderPID, o.Getenv)
		if err != nil {
			return res, wrap(err)
		}
		if !ok {
			return res, blocked(BlockNotYourSlot, "rota round return must run inside slot %s's worktree or hold the round lease", o.Slot)
		}
	}
	tolerate := tolerateMissing(&res.Warnings, id)
	res.Issue = id
	if it, err := be.Get(id); tolerate("lookup", err) != nil {
		return res, wrap(err)
	} else if err == nil {
		res.Issue = it.ID
	}

	p, err := e.Park(ctx, root, o.Slot, "return")
	if err != nil {
		return res, wrap(err)
	}
	res.Branch, res.Head, res.Salvaged = p.Branch, p.Head, p.Salvaged
	rnd := registryRound(root)
	h := handoff{verb: "return", from: o.Slot, round: rnd, branch: p.Branch, head: p.Head, salvaged: p.Salvaged, reason: o.Reason, note: o.Note}
	cid, _, err := h.post(be, id)
	if tolerate("handoff comment", err) != nil {
		return res, wrap(err)
	}
	res.CommentID = cid

	claimID := firstNonEmpty(s.ClaimID(), o.Slot+"@"+strconv.Itoa(rnd))
	if res.Released, err = releaseClaims(be, id, o.Slot, claimID, false); tolerate("claim release", err) != nil {
		return res, wrap(err)
	}
	if _, err := be.SetState(id, "none"); tolerate("state reset", err) != nil {
		return res, wrap(err)
	}
	if err := freeSlot(root, o.Slot, false); err != nil {
		return res, wrap(err)
	}
	res.Changed = true
	return res, nil
}

// ---- reclaim ----------------------------------------------------------------

// SlotHealth is the fresh health of one slot.
type SlotHealth struct {
	Health string
	Issue  string
	// Alive: the host shows a live agent for the slot. Known is false when the
	// host could not be asked, so liveness is unknown and read as alive.
	Alive, Known bool
	Stall        Stall
}

// Health computes a slot's health the way reconcile does, from the host's live
// agents, the slot's activity and its escalations. idle: the slot holds no
// issue. dead: the host agent is gone (a recorded handle with no agent, or
// state dead). stalled: alive and nothing moved for StallMinutes. healthy: the
// rest, including a slot waiting on an escalation.
func (e Env) Health(ctx context.Context, root string, s *worker.Slot, now time.Time) SlotHealth {
	h := SlotHealth{Issue: slotIssue(s), Health: HealthIdle}
	if h.Issue == "" {
		return h
	}
	name, wt, tab := s.Name(), s.Worktree(), s.Handle()
	if e.Snapshot != nil {
		if agents, err := e.Snapshot(ctx); err == nil {
			h.Known = true
			h.Alive = matchAgent(agents, name, tab, wt) >= 0
		}
	}
	if s.State() == "dead" || (h.Known && !h.Alive && tab != "") {
		h.Health = HealthDead
		return h
	}
	waiting := false
	for _, x := range escalation.Load(root) {
		if x.Status == escalation.StatusPending && x.Slot == name {
			waiting = true
		}
	}
	h.Stall = e.Stalled(ctx, StallInput{
		Worktree: wt, Base: firstNonEmpty(s.Base(), e.Base), Holds: true,
		Alive: h.Alive || !h.Known, Escalated: waiting, ActiveAt: s.ActiveAt(), Minutes: e.StallMinutes,
	}, now)
	h.Health = HealthHealthy
	if h.Stall.Stalled {
		h.Health = HealthStalled
	}
	return h
}

// ReclaimOpts are the flags of `rota round reclaim`.
type ReclaimOpts struct {
	Slot      string
	Force     bool
	Note      string
	HolderPID int
	Getenv    func(string) string
}

// Reclaimed is what Reclaim did.
type Reclaimed struct {
	Slot, Issue, Branch, Head string
	Health                    string
	Salvaged, Released        bool
	Parked                    bool
	Changed                   bool
	Warnings                  []string
}

// unresolvable says whether err is the tracker not knowing the issue: a slot
// whose task was minted in file mode (`B31`) and never mapped has no issue to
// comment on or release a claim from.
func unresolvable(err error) bool {
	var te *tracker.Error
	return errors.Is(err, backlog.ErrNotFound) || (errors.As(err, &te) && te.Kind == tracker.KindNotFound)
}

// tolerateMissing returns a func that swallows an unresolvable-issue error from
// a tracker step on issue, recording a warning instead, so a slot is never
// stranded busy by an issue the tracker no longer knows. Any other error passes.
func tolerateMissing(warnings *[]string, issue string) func(step string, err error) error {
	return func(step string, err error) error {
		if err == nil || !unresolvable(err) {
			return err
		}
		*warnings = append(*warnings, fmt.Sprintf("%s on %s skipped, the tracker does not resolve it: %v", step, issue, err))
		return nil
	}
}

// Reclaim is the orchestrator's verb for a slot that is dead or stalled: it
// kills a live pane, parks the worktree, posts the handoff comment, releases
// every claim of the slot, clears the issue's state and frees the slot with its
// handle. It does not reassign.
func (e Env) Reclaim(ctx context.Context, root string, be Board, o ReclaimOpts) (res Reclaimed, err error) {
	s := worker.LoadRegistry(root).Slot(o.Slot)
	if s == nil {
		return res, &exitcode.Error{Exit: exitcode.ExitResolution, Message: fmt.Sprintf("slot %s is not in the pool", o.Slot)}
	}
	res.Slot = o.Slot
	ok, err := e.holdsLease(ctx, root, o.HolderPID, o.Getenv)
	if err != nil {
		return res, wrap(err)
	}
	if !ok {
		return res, blocked(BlockNoRound, "this process holds no round lease: run rota round start first")
	}
	now := time.Now
	if e.Now != nil {
		now = e.Now
	}
	h := e.Health(ctx, root, s, now())
	res.Health, res.Issue = h.Health, h.Issue
	if h.Health == HealthIdle {
		return res, nil
	}
	if h.Health == HealthHealthy && !o.Force {
		return res, blocked(BlockHealthy, "slot %s is healthy (an agent is working on #%s); --force reclaims it anyway", o.Slot, h.Issue)
	}
	reason := "forced"
	switch h.Health {
	case HealthDead:
		reason = "dead"
	case HealthStalled:
		reason = fmt.Sprintf("stalled %d min", int(h.Stall.Idle.Minutes()))
	}
	// A live pane must be gone before its worktree moves under it. A dead
	// slot's agent is gone, but its pane may linger as a bare shell.
	if h.Health == HealthDead {
		e.workerEnv().SweepSlot(ctx, root, o.Slot)
	} else {
		switch {
		case e.HostName == host.Solo: // a subagent has no pane to close
		case h.Known && !h.Alive: // provably gone
		case !h.Known:
			return res, blocked(BlockLiveAgent, "the host cannot be asked, so slot %s's agent cannot be proved gone; start the host or close its pane", o.Slot)
		default:
			if err := e.workerEnv().KillSlot(ctx, root, o.Slot); err != nil {
				return res, wrap(err)
			}
		}
	}
	p, err := e.Park(ctx, root, o.Slot, "reclaim")
	if err != nil {
		return res, wrap(err)
	}
	res.Branch, res.Head, res.Salvaged, res.Parked = p.Branch, p.Head, p.Salvaged, p.Moved
	rnd := registryRound(root)
	hf := handoff{verb: "reclaim", from: o.Slot, round: rnd, branch: p.Branch, head: p.Head, salvaged: p.Salvaged, reason: "reclaimed, " + reason, note: o.Note}
	tolerate := tolerateMissing(&res.Warnings, h.Issue)
	if _, _, err := hf.post(be, h.Issue); tolerate("handoff comment", err) != nil {
		return res, wrap(err)
	}
	if res.Released, err = releaseClaims(be, h.Issue, o.Slot, s.ClaimID(), true); tolerate("claim release", err) != nil {
		return res, wrap(err)
	}
	if _, err := be.SetState(h.Issue, "none"); tolerate("state reset", err) != nil {
		return res, wrap(err)
	}
	if err := freeSlot(root, o.Slot, true); err != nil {
		return res, wrap(err)
	}
	res.Changed = true
	return res, nil
}

// ---- transfer ---------------------------------------------------------------

// TransferOpts are the flags of `rota round transfer`.
type TransferOpts struct {
	Issue, To     string
	Note          string // the text of --note-file
	BodyFile      string // decisions already settled, passed verbatim; "" for none
	AcceptOverlap bool
	HolderPID     int
	// Tier and TierReason are the receiver's tier ("" is round.tier); one above
	// the default needs a reason, as in assign. Unused when To is the human.
	Tier, TierReason string
	Settings         roundcfg.Settings
	Getenv           func(string) string
}

// Transferred is what Transfer did.
type Transferred struct {
	Issue, From, To, Branch, Head string
	Salvaged                      bool
	ClaimID                       string
	Dispatched, Changed           bool
	Warnings                      []string
	// Host, Brief and Worktree are set under solo (C8) in place of a dispatch.
	Host, Brief, Worktree string
}

// Transfer is the orchestrator's verb to move an issue from the slot holding it
// to another slot, or to the human. To a slot: the sender is parked, the
// handoff comment posted, the old claim released and a new one taken, the
// pushed branch is checked out in the receiver's worktree and the receiver is
// dispatched. To the human: no claim and nothing dispatched, the needs-human
// label goes on. If the dispatch fails the claim and branch stay with the
// receiver and the same call resumes it.
func (e Env) Transfer(ctx context.Context, root string, be Board, o TransferOpts) (res Transferred, err error) {
	set := o.Settings
	toHuman := o.To == HumanTarget
	if o.To == "" || (!toHuman && !slices.Contains(set.Roster, o.To)) {
		return res, usage("--to must be %s or a roster slot (%s)", HumanTarget, strings.Join(set.Roster, ", "))
	}
	if o.Tier != "" && !roundcfg.ValidTier(o.Tier) {
		return res, usage("--tier must be one of %s", strings.Join(roundcfg.Tiers, ", "))
	}
	reason := strings.TrimSpace(o.TierReason)
	if !toHuman && roundcfg.TierRank(o.Tier) > roundcfg.TierRank(set.Tier) && reason == "" {
		return res, usage("--tier %s is above the default tier %s: say why with --tier-reason", o.Tier, set.Tier)
	}
	if o.BodyFile != "" {
		if _, err := os.Stat(o.BodyFile); err != nil {
			return res, usage("--body-file %s: %v", o.BodyFile, err)
		}
	}
	it, err := be.Get(o.Issue)
	if err != nil {
		return res, wrap(err)
	}
	if it.Closed {
		return res, &exitcode.Error{Exit: exitcode.ExitResolution, Message: fmt.Sprintf("%s is closed", o.Issue)}
	}
	id := it.ID
	res.Issue, res.To = id, o.To
	ok, err := e.holdsLease(ctx, root, o.HolderPID, o.Getenv)
	if err != nil {
		return res, wrap(err)
	}
	if !ok {
		return res, blocked(BlockNoRound, "this process holds no round lease: run rota round start first")
	}
	rnd := registryRound(root)

	// Who holds it. A receiver that already holds it and was never dispatched
	// (idle, its new claim recorded) is a transfer left half done: resume it.
	reg := worker.LoadRegistry(root)
	var sender, receiver *worker.Slot
	for _, s := range reg.Slots() {
		if slotIssue(s) != strings.ToUpper(id) {
			continue
		}
		if s.Name() == o.To {
			receiver = s
		} else if sender == nil {
			sender = s
		}
	}
	resuming := receiver != nil && sender == nil && receiver.State() == "idle" &&
		receiver.ClaimID() == o.To+"@"+strconv.Itoa(rnd)
	// rec: no slot holds the issue but a queued PR does (its slot moved on, or
	// a transfer from it was left half done).
	var rec *worker.QueuedPR
	if sender == nil {
		rec = reg.QueuedIssue(id)
	}
	switch {
	case resuming:
	case receiver != nil && sender == nil:
		return res, blocked(BlockSameSlot, "slot %s already holds %s", o.To, id)
	case sender == nil && rec == nil:
		return res, &exitcode.Error{Exit: exitcode.ExitResolution, Message: fmt.Sprintf("no slot holds %s", id)}
	}
	switch {
	case sender != nil:
		res.From = sender.Name()
	case rec != nil:
		res.From = rec.From
	default:
		res.From = o.To
	}

	var to *worker.Slot
	queueTo := false // the receiver holds a PR that can wait in the queue
	if !toHuman {
		if to = reg.Slot(o.To); to == nil {
			return res, &exitcode.Error{Exit: exitcode.ExitResolution, Message: fmt.Sprintf("slot %s is not provisioned: run rota round start", o.To)}
		}
		if !resuming {
			if h := slotIssue(to); h != "" {
				ok, why := e.parkable(ctx, to)
				if !ok {
					return res, blocked(BlockSlotBusy, "%s", busyMsg(o.To, h, why))
				}
				queueTo = true
			}
		}
	} else if e.Forge == nil || it.Number == 0 {
		return res, unavailable("handing %s to the human needs the issue tracker (the %s label); this project has none", id, firstNonEmpty(e.NeedsHuman, DefaultNeedsHuman))
	}

	// Checks before anything moves: the brief, the overlap, the receiver's guard.
	var brief string
	if !toHuman {
		if brief, ok = briefPath(root, set, o.Getenv); !ok {
			return res, blocked(BlockBriefMissing, "the worker contract (skills/references/worker-contract.md) was not found; set round.brief")
		}
		if !resuming {
			tracked := e.trackedFiles(ctx, root)
			settled := ""
			if o.BodyFile != "" {
				b, _ := os.ReadFile(o.BodyFile)
				settled = string(b)
			}
			r, err := AssessBrief(be, id, tracked, set.SharedPaths, e.InFlightItems(ctx, root, be, tracked, set.SharedPaths), o.AcceptOverlap, settled)
			if err != nil {
				return res, wrap(err)
			}
			if ov := overlapCheck(r); ov != nil && !ov.OK {
				blk := blocked(BlockOverlap, "%s overlaps work in flight: %s", id, ov.Name+" ("+strings.Join(ov.Detail, "; ")+")")
				blk.Readiness = &r
				return res, blk
			}
			// A receiver about to be queued holds unmerged work the guard would
			// refuse; parking it clears that.
			if !queueTo {
				if _, err := e.workerEnv().ResetTo(root, o.To, id, BranchName(o.To, id, it.Title), true); err != nil {
					var we *exitcode.Error
					if errors.As(err, &we) && we.Data != nil { // the reset guard's refusal
						return res, blocked(BlockSlotBusy, "%s", we.Message)
					}
					return res, wrap(err)
				}
			}
		}
	}
	// The moves run as steps (saga.go). Transfer only goes forward: once the
	// sender is parked and its claim released there is nothing to give back, so
	// no step but a failed dispatch compensates, and a repeated call resumes.
	var (
		branch, oldClaim string
		p                Parked
		tmpName, text    string
	)
	from := res.From
	if resuming {
		branch = receiver.Branch()
		res.Branch = branch
		res.Head = e.headLine(ctx, receiver.Worktree(), "HEAD")
		res.ClaimID = receiver.ClaimID()
	}
	tolerate := tolerateMissing(&res.Warnings, id)
	moved := func() bool { return resuming }
	steps := []step{
		{name: "queue the receiver's PR", skip: func() bool { return !queueTo }, do: func() error {
			return wrap(e.queuePR(ctx, root, be, o.To))
		}},
		{name: "park the sender", skip: moved, do: func() (err error) {
			oldClaim = from + "@" + strconv.Itoa(rnd)
			if sender != nil {
				if p, err = e.Park(ctx, root, from, "transfer"); err != nil {
					return wrap(err)
				}
				oldClaim = firstNonEmpty(sender.ClaimID(), oldClaim)
			} else { // a queued PR: already pushed, nothing to park
				p.Branch = rec.Branch
				p.Head = e.queuedHead(ctx, root, p.Branch)
				oldClaim = firstNonEmpty(rec.ClaimID, oldClaim)
			}
			res.Branch, res.Head, res.Salvaged = p.Branch, p.Head, p.Salvaged
			branch = p.Branch
			return nil
		}},
		{name: "handoff comment", skip: moved, do: func() error {
			h := handoff{verb: "transfer", from: from, round: rnd, branch: p.Branch, head: p.Head, salvaged: p.Salvaged,
				reason: "transferred to " + o.To, note: o.Note}
			_, _, err := h.post(be, id)
			return wrap(tolerate("handoff comment", err))
		}},
		{name: "release the old claim", skip: moved, do: func() error {
			_, err := releaseClaims(be, id, from, oldClaim, false)
			return wrap(tolerate("claim release", err))
		}},
	}
	if toHuman {
		steps = append(steps,
			step{name: "reset the state", do: func() error {
				_, err := be.SetState(id, "none")
				return wrap(tolerate("state reset", err))
			}},
			step{name: "needs-human label", do: func() error {
				return wrap(e.Forge.AddLabels(ctx, it.Number, []string{firstNonEmpty(e.NeedsHuman, DefaultNeedsHuman)}, true))
			}},
			step{name: "free the sender", do: func() error {
				if sender != nil {
					return wrap(freeSlot(root, from, false))
				}
				return wrap(worker.RemoveQueuedPR(root, rec.PR))
			}})
		if err := runSteps(steps); err != nil {
			return res, err
		}
		res.Changed = true
		return res, nil
	}

	// A queued PR is still the issue's PR: the receiver takes it and its relay
	// log over once dispatched (dispatch resets both for a new task), and the
	// record goes. Until then it stays, so a failed dispatch resumes with it.
	adopt := func() error {
		if rec == nil {
			return nil
		}
		pr, relays := rec.PR, append([]any{}, rec.Relays...)
		if err := editSlot(root, o.To, func(s *worker.Slot) error {
			s.SetPR(pr)
			s.AppendRelays(relays)
			return nil
		}); err != nil {
			return err
		}
		return worker.RemoveQueuedPR(root, pr)
	}

	solo := isSolo(root)
	// A transferred worker starts on the default tier unless --tier says higher.
	kind, tier := harness.Claude, firstNonEmpty(o.Tier, o.Settings.Tier)
	model := o.Settings.Model(kind, tier)
	steps = append(steps,
		step{name: "claim", skip: moved, do: func() error {
			claimID := o.To + "@" + strconv.Itoa(rnd)
			won, holder, err := be.Claim(id, claimID)
			if err != nil {
				return wrap(err)
			}
			if !won {
				return blocked(BlockClaimed, "%s is claimed by %s", id, holder)
			}
			res.ClaimID = claimID
			return nil
		}},
		step{name: "free the sender", skip: func() bool { return resuming || sender == nil }, do: func() error {
			return wrap(freeSlot(root, from, false))
		}},
		step{name: "check out the branch", skip: moved, do: func() error {
			if err := e.checkout(ctx, root, to, id, it.Title, &branch); err != nil {
				return wrap(err)
			}
			res.Branch = branch
			return nil
		}},
		step{name: "bind the receiver", skip: moved, do: func() error {
			err := editSlot(root, o.To, func(s *worker.Slot) error {
				s.Bind(worker.Binding{Task: id, ClaimID: res.ClaimID, Kind: kind, Tier: tier, Model: model, TierReason: reason})
				s.SetBranch(branch)
				return nil
			})
			if err == nil {
				res.Changed = true
			}
			return wrap(err)
		}},
		step{name: "account", skip: func() bool { return solo || e.Accounts == nil || len(worker.Configured(root)) == 0 }, do: func() error {
			_, err := e.pickAccount(ctx, root, o.To)
			return wrap(err)
		}},
		step{name: "write the brief", do: func() error {
			decisions := ""
			if o.BodyFile != "" {
				b, _ := os.ReadFile(o.BodyFile)
				decisions = string(b)
			}
			text = pointerBrief(o.To, id, branch, brief, nil, decisions, outOfScope(be, id), tierBrief{Kind: kind, Tier: tier, Model: model, Default: o.Settings.Tier, Reason: reason, Table: o.Settings.Models[kind]})
			text += fmt.Sprintf("\nThis issue was handed to you by %s. Read its latest rota:handoff comment first (it ends with a `%s` marker), then continue from the pushed work on %s, already checked out in your worktree.\n",
				res.From, marker.Handoff(res.From, rnd), branch)
			if rec != nil {
				text += fmt.Sprintf("Its PR %s is already open: push to the branch to update it instead of opening another.\n", rec.PR)
			}
			if solo {
				return nil
			}
			tmp, err := os.CreateTemp("", "rota-round-brief-")
			if err != nil {
				return wrap(err)
			}
			tmpName = tmp.Name()
			tmp.WriteString(text)
			return wrap(tmp.Close())
		}})
	if solo {
		// No pane: mark the receiver busy and hand the brief back.
		steps = append(steps, step{name: "solo hand-off", do: func() error {
			b, wt, err := e.soloHandOff(root, o.To, text, rnd)
			if err != nil {
				return wrap(err)
			}
			res.Host, res.Brief, res.Worktree = host.Solo, b, wt
			res.Changed = true
			return nil
		}})
	} else {
		steps = append(steps, step{name: "dispatch", keep: true, do: func() error {
			if _, err := e.workerEnv().Dispatch(ctx, root, worker.DispatchOpts{Slot: o.To, BodyFile: tmpName, Task: id, Round: &rnd, Branch: branch, Model: model}); err != nil {
				// The claim and branch stay with the receiver; idle marks the
				// transfer as not delivered, so the same call resumes it.
				editSlot(root, o.To, func(s *worker.Slot) error { return s.MarkState("idle", "") })
				return err
			}
			res.Dispatched, res.Changed = true, true
			return nil
		}})
	}
	steps = append(steps, step{name: "adopt the queued PR", do: func() error { return wrap(adopt()) }})
	err = runSteps(steps)
	if tmpName != "" {
		os.Remove(tmpName)
	}
	return res, err
}

// queuedHead is the tip of a queued PR's branch: origin's, else the local one.
func (e Env) queuedHead(ctx context.Context, root, branch string) string {
	if h := e.headLine(ctx, root, "origin/"+branch); h != "" {
		return h
	}
	return e.headLine(ctx, root, branch)
}

func overlapCheck(r Readiness) *Check {
	for i := range r.Checks {
		if r.Checks[i].Name == CheckOverlap {
			return &r.Checks[i]
		}
	}
	return nil
}

func (e Env) headLine(ctx context.Context, wt, ref string) string {
	out, _, code := e.gitOut(ctx, wt, "log", "-1", "--abbrev=7", "--format=%h %s", ref, "--")
	if code != 0 {
		return ""
	}
	return out
}

// checkout puts the pushed work branch into the receiver's worktree: after the
// reset guard's clean check, `git switch -C <branch> origin/<branch>`. With no
// work branch to continue (the sender was parked already) it cuts the issue's
// usual branch from the base instead.
func (e Env) checkout(ctx context.Context, root string, to *worker.Slot, id, title string, branch *string) error {
	name, wt := to.Name(), to.Worktree()
	if *branch == "" || strings.HasPrefix(*branch, "park/") || *branch == to.Base() {
		*branch = BranchName(name, id, title)
		_, err := e.workerEnv().ResetTo(root, name, id, *branch, false)
		return err
	}
	if _, err := e.workerEnv().ResetTo(root, name, id, *branch, true); err != nil {
		return err
	}
	if cur, _, _ := e.gitOut(ctx, wt, "symbolic-ref", "--short", "-q", "HEAD"); cur == *branch {
		return nil
	}
	start := "origin/" + *branch
	if _, _, code := e.gitOut(ctx, wt, "rev-parse", "--verify", "-q", "refs/remotes/"+start); code != 0 {
		start = *branch
	}
	if _, errOut, code := e.gitOut(ctx, wt, "switch", "-q", "-C", *branch, start); code != 0 {
		return unavailable("could not check out %s in %s: %s", *branch, filepath.Base(wt), errOut)
	}
	return nil
}
