package round

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/escalation"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/exitmap"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/labels"
	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/skills"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// C10 (#76): moving an issue that is already assigned. The tracker changes
// first and the registry second, so a crash between them leaves a
// claim-mismatch that reconcile reports; every step is skipped when already
// done, so a repeated call finishes the rest.

// Blocked reasons of return, transfer and reclaim (exit 4, `blockedBy`).
const (
	BlockNotYourSlot = blockedby.NotYourSlot
	BlockSameSlot    = blockedby.SameSlot
	BlockHealthy     = blockedby.Healthy
	BlockLiveAgent   = blockedby.LiveAgent
	// BlockUnverifiable: the host could not vouch for the slot, which is not
	// proof the agent is gone (#820).
	BlockUnverifiable = blockedby.Unverifiable
)

// Slot health, as reclaim reports it.
const (
	HealthDead    = "dead"
	HealthStalled = "stalled"
	HealthHealthy = "healthy"
	HealthIdle    = "idle"
	// HealthUnverifiable: the slot's last poll could not reach the host or read
	// the pane, and the host cannot be asked now. Not dead.
	HealthUnverifiable = "unverifiable"
)

// HumanTarget is the `--to` value that hands an issue to the human.
const HumanTarget = "human"

// wrap maps what a step failed with onto the exit table: a missing item is 3,
// a busy registry lock is 6, git, the host and the tracker (a tracker 404
// included) are 5.
func wrap(err error) error {
	var blk *BlockedError
	if errors.As(err, &blk) {
		return err
	}
	_, out := exitmap.Translate(err, exitmap.Options{Classes: exitmap.BacklogNotFound | exitmap.Lock, Default: exitcode.ExitUnavailable})
	return out
}

func registryRound(root string) int {
	n, _ := worker.LoadRegistryTolerant(root).Round() // no error return; the verbs read the registry strictly themselves
	return n
}

// holdsLease: this process is the orchestrator the lease names.
func (e Env) holdsLease(ctx context.Context, root string, pid int) (bool, error) {
	_, held, err := e.heldLease(ctx, root, pid)
	return held, err
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

// ---- release a slot ---------------------------------------------------------

// slotRelease is the one release-slot sequence Return and Reclaim share: park
// the worktree, post the handoff comment, give the claim back, reset the
// issue's state, free the slot. The two verbs differ in two places, both on
// purpose:
//
//   - gone: Reclaim's worker is dead or killed, so the slot also drops its pane
//     handle and gives its port block back (the next dispatch picks one). Return's
//     worker is alive and keeps working the pool, so the slot keeps both.
//   - sweep: Reclaim runs because the registry may be wrong about the slot, so it
//     releases every open claim of `<slot>@`, not only the recorded one. Return
//     trusts the registry's claim, which the slot's own worker (or the lease
//     holder) wrote.
type slotRelease struct {
	root, slot, id, verb string
	be                   Board
	round                int
	reason, note         string
	claimID              string
	sweep, gone          bool
	tolerate             func(step string, err error) error
	// members are the bundle's issues beside id, released with it; tolerateOf is
	// the tolerate of one of them (nil: tolerate, the lead's, serves every one).
	members    []string
	tolerateOf func(id string) func(step string, err error) error
}

// released is what the sequence did, filled in as its steps run.
type released struct {
	Park      Parked
	CommentID string
	Released  bool
}

// releaseSteps is the sequence as steps for runSteps. Park and the handoff
// comment have no undo: the branch is pushed and the comment is deduplicated, so
// a repeated call finishes the rest. A later failure gives the claim back and
// restores the in-progress state, so the registry (which still binds the issue
// until the last step) and the tracker agree. A bundle's members (read from the
// slot before the park unbinds them) are released with the lead, each with the
// lead's comment, claim id and undo, in the same step order.
func (e Env) releaseSteps(ctx context.Context, r slotRelease, out *released) []step {
	ids := append([]string{r.id}, r.members...)
	tol := func(id string) func(string, error) error {
		if id != r.id && r.tolerateOf != nil {
			return r.tolerateOf(id)
		}
		return r.tolerate
	}
	label := func(name, id string) string {
		if id == r.id {
			return name
		}
		return name + " of " + id
	}
	steps := []step{{name: "park", do: func() (err error) {
		out.Park, err = e.Park(ctx, r.root, r.slot, r.verb)
		return wrap(err)
	}}}
	for _, id := range ids {
		steps = append(steps, step{name: label("handoff comment", id), do: func() error {
			h := handoff{verb: r.verb, from: r.slot, round: r.round, branch: out.Park.Branch, head: out.Park.Head,
				salvaged: out.Park.Salvaged, reason: r.reason, note: r.note}
			cid, _, err := h.post(r.be, id)
			if err = tol(id)("handoff comment", err); err != nil {
				return wrap(err)
			}
			if id == r.id {
				out.CommentID = cid
			}
			return nil
		}})
	}
	for _, id := range ids {
		got := false
		steps = append(steps, step{name: label("release the claim", id), do: func() (err error) {
			got, err = releaseClaims(r.be, id, r.slot, r.claimID, r.sweep)
			if id == r.id {
				out.Released = got
			}
			return wrap(tol(id)("claim release", err))
		}, undo: func() error {
			// Only the recorded claim can be taken back; a swept claim the registry
			// never knew has no id to restore.
			if !got || r.claimID == "" {
				return nil
			}
			_, _, err := r.be.Claim(id, r.claimID)
			return err
		}})
	}
	for _, id := range ids {
		cleared := false
		steps = append(steps, step{name: label("reset the state", id), do: func() (err error) {
			cleared, err = clearState(r.root, r.be, id, r.slot)
			return wrap(tol(id)("state reset", err))
		}, undo: func() error {
			if !cleared {
				return nil
			}
			_, err := r.be.SetState(id, "in-progress")
			return err
		}})
	}
	return append(steps, step{name: "free the slot", do: func() error {
		// The freed slot forgets its PR and relay log, so the gate would read the
		// PR as external and fail a cited relay. Keep the log where only the gate
		// reads it; the issue is released, so no queued record holds it (#1853).
		return wrap(worker.Update(r.root, func(doc *worker.Doc) {
			s := doc.Slot(r.slot)
			if s == nil {
				return
			}
			doc.LogRelays(s.PR(), s.Relays())
			s.Park(r.gone)
			if r.gone {
				s.ReleasePort()
			}
		}))
	}})
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
	reg, err := worker.LoadRegistry(root)
	if err != nil {
		return res, err
	}
	s := reg.Slot(o.Slot)
	if s == nil {
		return res, &exitcode.Error{Exit: exitcode.ExitResolution, Message: fmt.Sprintf("slot %s is not in the pool", o.Slot)}
	}
	id := s.HeldID()
	if id == "" {
		return res, &exitcode.Error{Exit: exitcode.ExitResolution, Message: fmt.Sprintf("slot %s holds no issue", o.Slot)}
	}
	res.Slot = o.Slot
	if !o.InSlot {
		ok, err := e.holdsLease(ctx, root, o.HolderPID)
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

	rnd := registryRound(root)
	var out released
	err = runSteps(e.releaseSteps(ctx, slotRelease{
		root: root, be: be, slot: o.Slot, id: id, verb: "return", round: rnd,
		reason: o.Reason, note: o.Note,
		claimID:  strutil.FirstNonEmpty(s.ClaimID(), o.Slot+"@"+strconv.Itoa(rnd)),
		tolerate: tolerate, members: s.Bundle(),
		tolerateOf: func(id string) func(string, error) error { return tolerateMissing(&res.Warnings, id) },
	}, &out))
	res.Branch, res.Head, res.Salvaged = out.Park.Branch, out.Park.Head, out.Park.Salvaged
	res.CommentID, res.Released = out.CommentID, out.Released
	if err != nil {
		return res, err
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
	// Reason is why an unverifiable slot is: host-unreachable, pane-unreadable
	// or stale-handle.
	Reason string
}

// Health computes a slot's health the way reconcile does, from the host's live
// agents, the slot's activity and its escalations. idle: the slot holds no
// issue. dead: the host agent is gone (a recorded handle with no agent, or
// state dead). stalled: alive and nothing moved for StallMinutes. healthy: the
// rest, including a slot waiting on an escalation.
func (e Env) Health(ctx context.Context, root string, s *worker.Slot, now time.Time) SlotHealth {
	h := SlotHealth{Issue: s.HeldID(), Health: HealthIdle}
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
	if worker.IsDead(s.State()) || (h.Known && !h.Alive && tab != "") {
		h.Health = HealthDead
		return h
	}
	// A host that answers settles it above (alive, or provably gone). One that
	// does not leaves the slot's last poll as the only word, and a poll that
	// could not reach the host is no proof of death or of life.
	if !h.Known && s.State() == "unverifiable" {
		h.Health = HealthUnverifiable
		h.Reason = unverifiableReason(s)
		return h
	}
	waiting := false
	for _, x := range escalation.Load(root) {
		if x.Status == escalation.StatusPending && x.Slot == name {
			waiting = true
		}
	}
	h.Stall = e.Stalled(ctx, StallInput{
		Worktree: wt, Base: strutil.FirstNonEmpty(s.Base(), e.Base), Holds: true,
		Alive: h.Alive || !h.Known, Escalated: waiting, ActiveAt: s.LastAlive(), Minutes: e.Round.StallMinutes,
	}, now)
	h.Health = HealthHealthy
	if h.Stall.Stalled {
		h.Health = HealthStalled
	}
	return h
}

// unverifiableReason is the reason the slot's last poll recorded,
// "unrecorded" when it left none.
func unverifiableReason(s *worker.Slot) string {
	if r := s.Reason(); r != "" {
		return r
	}
	return "unrecorded"
}

// ReclaimOpts are the flags of `rota round reclaim`.
type ReclaimOpts struct {
	Slot      string
	Force     bool
	Note      string
	HolderPID int
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
func unresolvable(err error) bool { return exitmap.IsNotFound(err) }

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
	reg, err := worker.LoadRegistry(root)
	if err != nil {
		return res, err
	}
	s := reg.Slot(o.Slot)
	if s == nil {
		return res, &exitcode.Error{Exit: exitcode.ExitResolution, Message: fmt.Sprintf("slot %s is not in the pool", o.Slot)}
	}
	res.Slot = o.Slot
	if err := fenceExternal(s, "reclaim", "move a worktree"); err != nil {
		return res, err
	}
	ok, err := e.holdsLease(ctx, root, o.HolderPID)
	if err != nil {
		return res, wrap(err)
	}
	if !ok {
		return res, blocked(BlockNoRound, "this process holds no round lease: run rota round start first")
	}
	now := e.Worker.Now
	h := e.Health(ctx, root, s, now())
	res.Health, res.Issue = h.Health, h.Issue
	if h.Health == HealthIdle {
		return res, nil
	}
	if h.Health == HealthHealthy && !o.Force {
		return res, blocked(BlockHealthy, "slot %s is healthy (an agent is working on %s); --force reclaims it anyway", o.Slot, tracker.RefOf(h.Issue))
	}
	if h.Health == HealthUnverifiable && !o.Force {
		return res, blocked(BlockUnverifiable, "slot %s is unverifiable (%s): the host cannot say whether its agent lives, which is not proof it died. Inspect its pane, then wait or reclaim with --force", o.Slot, h.Reason)
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
		e.Worker.SweepSlot(ctx, root, o.Slot)
	} else {
		switch {
		case e.HostName == host.Solo: // a subagent has no pane to close
		case h.Known && !h.Alive: // provably gone
		case !h.Known:
			why := ""
			if h.Health == HealthUnverifiable {
				why = fmt.Sprintf(" (unverifiable: %s)", h.Reason)
			}
			return res, blocked(BlockLiveAgent, "the host cannot be asked, so slot %s's agent cannot be proved gone%s; start the host or close its pane", o.Slot, why)
		default:
			if err := e.Worker.KillSlot(ctx, root, o.Slot); err != nil {
				return res, wrap(err)
			}
		}
	}
	rnd := registryRound(root)
	var out released
	err = runSteps(e.releaseSteps(ctx, slotRelease{
		root: root, be: be, slot: o.Slot, id: h.Issue, verb: "reclaim", round: rnd,
		reason: "reclaimed, " + reason, note: o.Note,
		claimID: s.ClaimID(), sweep: true, gone: true,
		tolerate: tolerateMissing(&res.Warnings, h.Issue), members: s.Bundle(),
		tolerateOf: func(id string) func(string, error) error { return tolerateMissing(&res.Warnings, id) },
	}, &out))
	res.Branch, res.Head, res.Salvaged, res.Parked = out.Park.Branch, out.Park.Head, out.Park.Salvaged, out.Park.Moved
	res.Released = out.Released
	if err != nil {
		return res, err
	}
	res.Changed = true
	return res, nil
}

// ---- transfer ---------------------------------------------------------------

// TransferOpts are the flags of `rota round transfer`.
type TransferOpts struct {
	Issue, To     string
	Note          string // the text of --note-file
	Decisions     string // decisions already settled, passed verbatim; "" for none
	AcceptOverlap bool
	// Tier and TierReason are the receiver's tier ("" is round.tier); one above
	// the default needs a reason, as in assign. Unused when To is the human.
	Tier, TierReason string
	// CapReason says why an item at the bounce cap moves: ledger.CapAmbiguousCriteria
	// (to the human, after the criteria audit) or ledger.CapTier (to a stronger
	// worker) or ledger.CapFresh (to a fresh context at round.freshAt, below
	// the cap). "" is a transfer that is not a cap decision.
	CapReason string
}

// Transferred is what Transfer did.
type Transferred struct {
	Issue, From, To, Branch, Head string
	Salvaged                      bool
	ClaimID                       string
	Dispatched, Changed           bool
	Warnings                      []string
	// Host, Brief and Worktree are set under solo (C8) in place of a dispatch.
	Host            host.Kind
	Brief, Worktree string
}

// transferRun is what Transfer settled before it moved anything: who holds the
// issue, the receiver and its resolved worker. The steps fill branch, parked and
// oldClaim as they run.
type transferRun struct {
	it                *backlog.Item
	id, from          string
	toHuman, resuming bool
	queueTo           bool // the receiver holds a PR that can wait in the queue
	sender, receiver  *worker.Slot
	rec               *worker.QueuedPR // no slot holds the issue but a queued PR does
	// member: the issue is a bundle member, not a lead (transfer to the human
	// only). lead is its bundle's lead, holder the slot (or queued record) with it.
	member           bool
	lead             string
	holder           *worker.Slot
	holderRec        *worker.QueuedPR
	to               *worker.Slot
	kind, kindSource string
	model, tier      string // model is the applied one
	reason           string
	decisions, brief string
	decisionContext  string
	rnd              int

	branch, oldClaim string
	parked           Parked
}

// Transfer is the orchestrator's verb to move an issue from the slot holding it
// to another slot, or to the human. To a slot: the sender is parked, the
// handoff comment posted, the old claim released and a new one taken, the
// pushed branch is checked out in the receiver's worktree and the receiver is
// dispatched. To the human: no claim and nothing dispatched, the needs-human
// label goes on. If the dispatch fails the claim and branch stay with the
// receiver and the same call resumes it.
func (e Env) Transfer(ctx context.Context, root string, be Board, o TransferOpts) (res Transferred, err error) {
	if err := e.roundCfgErr(); err != nil {
		return res, err
	}
	defer func() {
		if err == nil && res.Changed {
			if lerr := e.Worker.LedgerAppendErr(root, ledger.Entry{Kind: ledger.KindTransfer, Issue: res.Issue, Slot: res.To,
				Detail: ledger.Detail("from", res.From, "capReason", o.CapReason)}); lerr != nil {
				err = fmt.Errorf("%s is transferred to %s, but the round ledger row was not written: %w", res.Issue, res.To, lerr)
			}
		}
	}()
	run, err := e.planTransfer(ctx, root, be, o, &res)
	if err != nil {
		return res, err
	}
	// The moves run as steps (saga.go). Transfer only goes forward: once the
	// sender is parked and its claim released there is nothing to give back, so
	// no step but a failed dispatch compensates, and a repeated call resumes.
	if run.member {
		if err := runSteps(e.transferMemberSteps(ctx, root, be, run, &res)); err != nil {
			return res, err
		}
		res.Changed = true
		return res, nil
	}
	if run.toHuman {
		if err := runSteps(e.transferToHumanSteps(ctx, root, be, o, run, &res)); err != nil {
			return res, err
		}
		res.Changed = true
		return res, nil
	}
	steps, d, err := e.transferToSlotSteps(ctx, root, be, o, run, &res)
	if err != nil {
		return res, wrap(err)
	}
	err = runSteps(steps)
	d.cleanup()
	res.Host, res.Brief, res.Worktree = d.Host, d.BriefText, d.Worktree
	res.Dispatched = res.Dispatched || d.Dispatched
	res.Changed = res.Changed || d.Changed
	return res, err
}

// checkFlags validates the flags that need no board or registry and returns
// whether the issue goes to the human and the tier reason.
func (o TransferOpts) checkFlags(set roundcfg.Settings) (toHuman bool, reason string, err error) {
	toHuman = o.To == HumanTarget
	if o.To == "" || (!toHuman && !slices.Contains(set.Roster, o.To)) {
		return false, "", usage("--to must be %s or a roster slot (%s)", HumanTarget, strings.Join(set.Roster, ", "))
	}
	switch {
	case o.CapReason == "":
	case o.CapReason == ledger.CapAmbiguousCriteria && !toHuman:
		return false, "", usage("--cap-reason %s means the criteria need the human: use --to %s", o.CapReason, HumanTarget)
	case o.CapReason == ledger.CapTier && toHuman:
		return false, "", usage("--cap-reason %s moves the item to a stronger worker: use --to <slot>", o.CapReason)
	case o.CapReason == ledger.CapFresh && toHuman:
		return false, "", usage("--cap-reason %s moves the item to a fresh worker: use --to <slot>", o.CapReason)
	case o.CapReason != ledger.CapAmbiguousCriteria && o.CapReason != ledger.CapTier && o.CapReason != ledger.CapFresh:
		return false, "", usage("--cap-reason must be %s, %s or %s", ledger.CapAmbiguousCriteria, ledger.CapTier, ledger.CapFresh)
	}
	if o.Tier != "" && !roundcfg.ValidTier(o.Tier) {
		return false, "", usage("--tier must be one of %s", strings.Join(roundcfg.Tiers, ", "))
	}
	reason = strings.TrimSpace(o.TierReason)
	if !toHuman && roundcfg.TierRank(o.Tier) > roundcfg.TierRank(set.Tier) && reason == "" {
		return false, "", usage("--tier %s is above the default tier %s: say why with --tier-reason", o.Tier, set.Tier)
	}
	return toHuman, reason, nil
}

// planTransfer makes every refusal Transfer can make before it moves anything
// and fills res as far as it got.
func (e Env) planTransfer(ctx context.Context, root string, be Board, o TransferOpts, res *Transferred) (*transferRun, error) {
	set := e.Round
	toHuman, reason, err := o.checkFlags(set)
	if err != nil {
		return nil, err
	}
	if err := e.refuseWhileGateRuns(ctx, root, "transfer"); err != nil {
		return nil, err
	}
	decisions := o.Decisions
	it, err := be.Get(o.Issue)
	if err != nil {
		return nil, wrap(err)
	}
	// A closed issue still holds its slot and claim when it was closed before it
	// was released: handing it to the human frees both, with no label (nothing
	// is left to do) and no reopen.
	if it.Closed && !toHuman {
		return nil, &exitcode.Error{Exit: exitcode.ExitResolution, Message: fmt.Sprintf("%s is closed", o.Issue)}
	}
	if it.Closed {
		res.Warnings = append(res.Warnings, fmt.Sprintf("%s is closed: released its slot and claim without the needs-human label", o.Issue))
	}
	id := it.ID
	res.Issue, res.To = id, o.To
	ok, err := e.holdsLease(ctx, root, e.HolderPID)
	if err != nil {
		return nil, wrap(err)
	}
	if !ok {
		return nil, blocked(BlockNoRound, "this process holds no round lease: run rota round start first")
	}
	run := &transferRun{it: it, id: id, toHuman: toHuman, reason: reason, decisions: decisions, rnd: registryRound(root)}
	rnd := run.rnd

	// Who holds it. A receiver that already holds it and was never dispatched
	// (idle, its new claim recorded) is a transfer left half done: resume it.
	reg, err := worker.LoadRegistry(root)
	if err != nil {
		return nil, err
	}
	// A bundle member leaves only to the human, on its own; it is no slot's task,
	// so it is looked up before "no slot holds".
	if hs, hr := reg.BundleHolder(id); hs != nil || hr != nil {
		if !toHuman {
			return nil, blocked(BlockBundle, "%s is bundled: a member leaves a bundle only to the human (--to %s)", id, HumanTarget)
		}
		if !e.forgeOn(be) {
			return nil, unavailable("handing %s to the human needs the issue tracker (the %s label); this project has none", id, e.NeedsHuman)
		}
		run.member, run.holder, run.holderRec = true, hs, hr
		if hs != nil {
			run.lead, run.from = hs.HeldID(), hs.Name()
		} else {
			run.lead, run.from = hr.Issue, hr.From
		}
		res.From = run.from
		return run, nil
	}
	// Two attempts of a best-of:2 issue still holding it: transfer cannot
	// choose one. After a pick, return or reclaim one holder is left.
	if b := reg.BestOf(id); b != nil {
		var holders []string
		for _, s := range reg.Slots() {
			// The receiver of a transfer left half done is not a second attempt.
			if s.HeldID() == worker.NormID(id) && s.Name() != o.To {
				holders = append(holders, s.Name())
			}
		}
		for _, q := range reg.PRs() {
			if queuedIssue(q) == worker.NormID(id) {
				holders = append(holders, labels.ReviewPrefix+q.From)
			}
		}
		if len(holders) > 1 {
			return nil, blocked(BlockBestOf, "%s is a best-of:2 issue held by %s: transfer cannot choose an attempt; pick, return or reclaim one first", id, strings.Join(holders, " and "))
		}
	}
	for _, s := range reg.Slots() {
		if s.HeldID() != worker.NormID(id) {
			continue
		}
		if s.Name() == o.To {
			run.receiver = s
		} else if run.sender == nil {
			run.sender = s
		}
	}
	sender, receiver := run.sender, run.receiver
	run.resuming = receiver != nil && sender == nil && worker.IsIdle(receiver.State()) &&
		receiver.ClaimID() == o.To+"@"+strconv.Itoa(rnd)
	resuming := run.resuming
	// rec: no slot holds the issue but a queued PR does (its slot moved on, or
	// a transfer from it was left half done).
	if sender == nil {
		run.rec = reg.QueuedIssue(id)
	}
	// A bundle's lead cannot move alone, and moving the whole bundle to another
	// slot is not built: its members go to the human first, or the slot is
	// returned or reclaimed.
	var bundle []string
	switch {
	case sender != nil:
		bundle = sender.Bundle()
	case run.rec != nil:
		bundle = run.rec.Bundle
	}
	if len(bundle) > 0 {
		refs := make([]string, len(bundle))
		for i, m := range bundle {
			refs[i] = tracker.RefOf(m)
		}
		return nil, blocked(BlockBundle, "%s leads a bundle with %s: transfer each member to the human first, or return/reclaim the slot to release the whole bundle", tracker.RefOf(id), strings.Join(refs, ", "))
	}
	switch {
	case resuming:
	case receiver != nil && sender == nil:
		return nil, blocked(BlockSameSlot, "slot %s already holds %s", o.To, id)
	case sender == nil && run.rec == nil:
		return nil, &exitcode.Error{Exit: exitcode.ExitResolution, Message: fmt.Sprintf("no slot holds %s", id)}
	}
	switch {
	case sender != nil:
		res.From = sender.Name()
	case run.rec != nil:
		res.From = run.rec.From
	default:
		res.From = o.To
	}
	run.from = res.From
	switch {
	case sender != nil:
		run.decisionContext = sender.BriefDecisionContext()
	case run.rec != nil:
		run.decisionContext = run.rec.DecisionContext
	case receiver != nil:
		run.decisionContext = receiver.BriefDecisionContext()
	}

	if err := fenceExternal(sender, "transfer", "move a worktree"); err != nil {
		return nil, err
	}
	if sender != nil && e.mergeInProgress(ctx, sender.Worktree()) {
		return nil, blocked(BlockMergeInProgress, "slot %s is mid-merge (MERGE_HEAD) in its worktree: wait for the worker to commit or abort it before moving %s", sender.Name(), id)
	}
	if !toHuman {
		if run.to = reg.Slot(o.To); run.to == nil {
			return nil, &exitcode.Error{Exit: exitcode.ExitResolution, Message: fmt.Sprintf("slot %s is not provisioned: run rota round start", o.To)}
		}
		if err := fenceExternal(run.to, "transfer", "check a branch out in a worktree"); err != nil {
			return nil, err
		}
		if err := checkActive(reg, set, o.To, resuming); err != nil {
			return nil, err
		}
		if !resuming {
			if h := run.to.HeldID(); h != "" {
				ok, why := e.parkable(ctx, run.to)
				if !ok {
					return nil, blocked(BlockSlotBusy, "%s", busyMsg(o.To, h, why))
				}
				run.queueTo = true
			}
		}
	} else if !e.forgeOn(be) {
		return nil, unavailable("handing %s to the human needs the issue tracker (the %s label); this project has none", id, e.NeedsHuman)
	}

	// A transferred worker starts on the default tier unless --tier says higher.
	// The preflight is assign's, without --kind or --model: the kind resolves
	// from the item's label, else the first round.harnesses entry, else the receiver's
	// recorded kind, else claude; the model from the item's label, else the
	// tier map. Headroom and scope are not asked of a receiver that resumes.
	run.kind, run.tier = harness.Default, strutil.FirstNonEmpty(o.Tier, e.Round.Tier)
	var model string
	if !toHuman {
		pf, err := e.preflight(ctx, root, be, preflightIn{Item: it, Settings: set, Tier: run.tier,
			SlotKind: run.to.HarnessKind(), Resuming: resuming, NoFallback: true})
		res.Warnings = append(res.Warnings, pf.Warnings...)
		if err != nil {
			return nil, err
		}
		run.kind, run.kindSource, model, run.model = pf.Kind, pf.KindSource, pf.Model, pf.AppliedModel
	}

	// Checks before anything moves: the brief, the receiver's launch, the
	// overlap, the receiver's guard.
	if !toHuman {
		if run.brief, ok = briefPath(root, set, e.getenv(), skills.CacheDir(e.getenv())); !ok {
			return nil, blocked(BlockBriefMissing, "the worker contract (skills/references/worker-contract.md) was not found; set round.brief")
		}
		// The receiver is refused as assign would refuse it, before anything moves.
		warns, err := e.deliveryRefusals(ctx, root, run.kind, o.To, model)
		if err != nil {
			return nil, wrap(err)
		}
		res.Warnings = append(res.Warnings, warns...)
		if !resuming {
			tracked := e.trackedFiles(ctx, root)
			r, err := AssessScoped(be, id, tracked, set.SharedPaths, e.InFlightItems(ctx, root, be, tracked, set.SharedPaths), o.AcceptOverlap, decisions, set.BlockScopeOverlap)
			if err != nil {
				return nil, wrap(err)
			}
			if ov := overlapCheck(r); ov != nil && !ov.OK {
				blk := blocked(BlockOverlap, "%s overlaps work in flight: %s", id, ov.Name+" ("+strings.Join(ov.Detail, "; ")+")")
				blk.Readiness = &r
				return nil, blk
			}
			// A receiver about to be queued holds unmerged work the guard would
			// refuse; parking it clears that.
			if !run.queueTo {
				if _, err := e.Worker.ResetTo(root, o.To, id, worker.BranchName(o.To, id, it.Title), true); err != nil {
					var we *exitcode.Error
					if errors.As(err, &we) && we.Data != nil { // the reset guard's refusal
						return nil, blocked(BlockSlotBusy, "%s", we.Message)
					}
					return nil, wrap(err)
				}
			}
		}
	}
	if resuming {
		run.branch = receiver.Branch()
		res.Branch = run.branch
		res.Head = e.headLine(ctx, receiver.Worktree(), "HEAD")
		res.ClaimID = receiver.ClaimID()
	}
	return run, nil
}

// mergeInProgress reports whether the worktree at wt holds an unfinished merge:
// its worker is resolving a conflict, and parking the slot would cut that off.
func (e Env) mergeInProgress(ctx context.Context, wt string) bool {
	if wt == "" {
		return false
	}
	_, _, code := e.gitOut(ctx, wt, "rev-parse", "-q", "--verify", "MERGE_HEAD")
	return code == 0
}

// transferHeadSteps is what both targets share: park the sender, say so on the
// issue and give the old claim up. A transfer left half done skips all three.
func (e Env) transferHeadSteps(ctx context.Context, root string, be Board, o TransferOpts, run *transferRun, res *Transferred) []step {
	id, from, rnd := run.id, run.from, run.rnd
	tolerate := tolerateMissing(&res.Warnings, id)
	moved := func() bool { return run.resuming }
	return []step{
		{name: "queue the receiver's PR", skip: func() bool { return !run.queueTo }, do: func() error {
			return wrap(e.queuePR(ctx, root, be, o.To))
		}},
		{name: "park the sender", skip: moved, do: func() (err error) {
			run.oldClaim = from + "@" + strconv.Itoa(rnd)
			if run.sender != nil {
				if run.parked, err = e.Park(ctx, root, from, "transfer"); err != nil {
					return wrap(err)
				}
				run.oldClaim = strutil.FirstNonEmpty(run.sender.ClaimID(), run.oldClaim)
			} else { // a queued PR: already pushed, nothing to park
				run.parked.Branch = run.rec.Branch
				run.parked.Head = e.queuedHead(ctx, root, run.parked.Branch)
				run.oldClaim = strutil.FirstNonEmpty(run.rec.ClaimID, run.oldClaim)
			}
			res.Branch, res.Head, res.Salvaged = run.parked.Branch, run.parked.Head, run.parked.Salvaged
			run.branch = run.parked.Branch
			return nil
		}},
		{name: "handoff comment", skip: moved, do: func() error {
			p := run.parked
			h := handoff{verb: "transfer", from: from, round: rnd, branch: p.Branch, head: p.Head, salvaged: p.Salvaged,
				reason: "transferred to " + o.To, note: o.Note}
			_, _, err := h.post(be, id)
			return wrap(tolerate("handoff comment", err))
		}},
		{name: "release the old claim", skip: moved, do: func() error {
			_, err := releaseClaims(be, id, from, run.oldClaim, false)
			return wrap(tolerate("claim release", err))
		}},
	}
}

// transferToHumanSteps is the sequence for --to human: the head, then the state
// reset, the needs-human label, the sender freed and the item clock stopped.
func (e Env) transferToHumanSteps(ctx context.Context, root string, be Board, o TransferOpts, run *transferRun, res *Transferred) []step {
	id, from := run.id, run.from
	tolerate := tolerateMissing(&res.Warnings, id)
	return append(e.transferHeadSteps(ctx, root, be, o, run, res),
		step{name: "reset the state", do: func() error {
			_, err := clearState(root, be, id, from)
			return wrap(tolerate("state reset", err))
		}},
		step{name: "needs-human label", skip: func() bool { return run.it.Closed }, do: func() error {
			return wrap(e.Forge.AddLabels(ctx, run.it.IssueKey(), []string{e.NeedsHuman}, true))
		}},
		step{name: "free the sender", do: func() error {
			if run.sender != nil {
				return wrap(freeSlot(root, from, false))
			}
			return wrap(worker.RemoveQueuedPR(root, run.rec.PR))
		}},
		step{name: "stop the item clock", do: func() error {
			return wrap(worker.ClearItemStart(root, worker.NormID(id)))
		}})
}

// transferMemberSteps is `--to human` for a bundle member: a note on the
// member, its claim and state given back, the needs-human label, and the member
// dropped from the bundle. The lead keeps its slot, branch and session; nothing
// is parked or dispatched. Forward only, like the other transfers.
func (e Env) transferMemberSteps(ctx context.Context, root string, be Board, run *transferRun, res *Transferred) []step {
	id, from := run.id, run.from
	tolerate := tolerateMissing(&res.Warnings, id)
	claim := from + "@" + strconv.Itoa(run.rnd)
	branch, head := "", ""
	if s := run.holder; s != nil {
		claim = strutil.FirstNonEmpty(s.ClaimID(), claim)
		branch, head = s.Branch(), e.headLine(ctx, s.Worktree(), "HEAD")
	} else {
		claim = strutil.FirstNonEmpty(run.holderRec.ClaimID, claim)
		branch = run.holderRec.Branch
		head = e.queuedHead(ctx, root, branch)
	}
	return []step{
		{name: "handoff comment", do: func() error {
			h := handoff{verb: "transfer", from: from, round: run.rnd, branch: branch, head: head,
				reason: fmt.Sprintf("left the bundle of %s on slot %s: transferred to the human", tracker.RefOf(run.lead), from)}
			_, _, err := h.post(be, id)
			return wrap(tolerate("handoff comment", err))
		}},
		{name: "release the claim", do: func() error {
			_, err := releaseClaims(be, id, from, claim, false)
			return wrap(tolerate("claim release", err))
		}},
		{name: "reset the state", do: func() error {
			_, err := clearState(root, be, id, from)
			return wrap(tolerate("state reset", err))
		}},
		{name: "needs-human label", skip: func() bool { return run.it.Closed }, do: func() error {
			return wrap(e.Forge.AddLabels(ctx, run.it.IssueKey(), []string{e.NeedsHuman}, true))
		}},
		{name: "leave the bundle", do: func() error {
			return wrap(worker.DropBundleMember(root, id))
		}},
		{name: "stop the item clock", do: func() error {
			return wrap(worker.ClearItemStart(root, worker.NormID(id)))
		}},
	}
}

// transferToSlotSteps is the sequence for a receiver slot: the head, the new
// claim, the sender freed, the branch checked out and the receiver bound, then
// the delivery (its cleanup is the caller's) and the adoption of a queued PR.
func (e Env) transferToSlotSteps(ctx context.Context, root string, be Board, o TransferOpts, run *transferRun, res *Transferred) ([]step, *delivery, error) {
	id, from, rnd := run.id, run.from, run.rnd
	sender, rec, receiver, resuming := run.sender, run.rec, run.receiver, run.resuming
	moved := func() bool { return resuming }
	steps := e.transferHeadSteps(ctx, root, be, o, run, res)

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

	// The receiver resuming its transfer keeps the attempt its brief named.
	attempt, attemptBase := "", ""
	if resuming {
		attempt, attemptBase = receiver.Attempt(), receiver.AttemptBase()
	}
	if attempt == "" {
		attempt = worker.NewAttempt()
	}
	d := &delivery{Slot: o.To, Task: id, Branch: func() string { return run.branch }, Round: rnd, Kind: run.kind, Model: run.model, Wrap: wrap, LogRelay: true,
		Brief: func() string {
			tryItSteps, tryItWhy := e.tryItBrief(ctx, root, be, id)
			text := pointerBrief(o.To, id, run.branch, run.brief, nil, run.decisions, run.decisionContext, outOfScope(be, id), touches(be, id), tryItSteps, tryItWhy, nil, tierBrief{Kind: run.kind, Tier: run.tier, Model: run.model, Default: e.Round.Tier, Reason: run.reason, Table: e.Round.Models[run.kind], Attempt: attempt, FullOnly: e.Round.FullOnly, TDD: e.Round.TDD})
			text += fmt.Sprintf("\nThis issue was handed to you by %s. Read its latest rota:handoff comment first (it ends with a `%s` marker), then continue from the pushed work on %s, already checked out in your worktree.\n",
				res.From, marker.Handoff(res.From, rnd), run.branch)
			if rec != nil {
				text += fmt.Sprintf("Its PR %s is already open: push to the branch to update it instead of opening another.\n", rec.PR)
			}
			return text
		}}
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
			if err := e.checkout(ctx, root, run.to, id, run.it.Title, &run.branch); err != nil {
				return wrap(err)
			}
			res.Branch = run.branch
			return nil
		}},
		step{name: "bind the receiver", skip: moved, do: func() error {
			if attemptBase == "" {
				attemptBase = e.branchTip(ctx, root, run.branch)
			}
			err := editSlot(root, o.To, func(s *worker.Slot) error {
				s.Bind(worker.Binding{Task: id, ClaimID: res.ClaimID, Kind: run.kind, KindSource: run.kindSource, Tier: run.tier, Model: run.model, TierReason: run.reason, Attempt: attempt, AttemptBase: attemptBase})
				s.SetBranch(run.branch)
				s.SetBriefContext(run.decisions, nil)
				s.SetBriefDecisionContext(run.decisionContext)
				s.SetAwaitingDone(true) // the branch's PR is the sender's: only this worker's own done counts
				return nil
			})
			if err == nil {
				res.Changed = true
			}
			return wrap(err)
		}})
	// The account, the pointer brief and the hand-off.
	ds, err := d.steps(ctx, e, root)
	if err != nil {
		return nil, d, err
	}
	steps = append(steps, ds...)
	return append(steps, step{name: "adopt the queued PR", do: func() error { return wrap(adopt()) }}), d, nil
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

// branchTip is the full sha of a work branch's tip, "" when it exists nowhere.
// A new attempt records it as the commits it inherited, which keep their
// earlier attempts' trailers. The branch is fetched first (the sender may have
// pushed since root's last fetch); of origin's and the local tip it takes the
// one that contains the other, origin's when they diverged.
func (e Env) branchTip(ctx context.Context, root, branch string) string {
	// The name can come from an issue comment (a handoff): refuse anything git
	// would read as an option or that is not a branch name.
	if branch == "" || strings.HasPrefix(branch, "-") {
		return ""
	}
	if _, _, code := e.gitOut(ctx, root, "check-ref-format", "--branch", branch); code != 0 {
		return ""
	}
	e.gitOut(ctx, root, "fetch", "-q", "origin", "--", "refs/heads/"+branch+":refs/remotes/origin/"+branch)
	rev := func(ref string) string {
		out, _, code := e.gitOut(ctx, root, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
		if code != 0 {
			return ""
		}
		return strings.TrimSpace(out)
	}
	remote, local := rev("origin/"+branch), rev(branch)
	if remote == "" {
		return local
	}
	if local != "" {
		if _, _, code := e.gitOut(ctx, root, "merge-base", "--is-ancestor", remote, local); code == 0 {
			return local
		}
	}
	return remote
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
	if *branch == "" || worker.IsPark(*branch) || *branch == to.Base() {
		*branch = worker.BranchName(name, id, title)
		_, err := e.Worker.ResetTo(root, name, id, *branch, false)
		return err
	}
	if _, err := e.Worker.ResetTo(root, name, id, *branch, true); err != nil {
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
