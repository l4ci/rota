package round

import (
	"context"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// Blocked reasons of pick (exit 4, `blockedBy`).
const (
	BlockNotBestOf      = "not best-of"
	BlockNotAttempt     = "not an attempt"
	BlockPicked         = "already picked"
	BlockAttemptRunning = "attempt running"
)

// Attempt states, as pick reports them.
const (
	AttemptOpenPR = "open-pr" // the attempt has a PR
	AttemptNoPR   = "no-pr"   // its slot still holds the issue and has no PR
	AttemptGone   = "gone"    // returned, reclaimed or moved on
)

// PickOpts are the flags of `rota round pick`.
type PickOpts struct {
	ID, PR, Reason string
	HolderPID      int
}

// PickAttempt is one best-of attempt as pick found it.
type PickAttempt struct{ Slot, Branch, PR, State string }

// Picked is what Pick did.
type Picked struct {
	ID string
	// Loser.Slot is "" when the record had one attempt only.
	Winner, Loser PickAttempt
	// Closed: the loser's PR was closed by this call.
	Closed, Changed bool
	Warnings        []string
}

// prCloser is the forge side of closing a PR with a comment.
type prCloser interface {
	PRClose(ctx context.Context, pr int, comment string) error
}

// pickView is one attempt with what pick needs to act on it.
type pickView struct {
	PickAttempt
	claimID string
	slot    *worker.Slot // the slot still holding the issue on this attempt, nil otherwise
}

func (v pickView) number() (int, bool) { return worker.PRRefNumber(v.PR) }

// Pick is the orchestrator's verb for a best-of:2 issue: it names the attempt
// whose PR may merge and retires the other one: its PR is closed with the
// reason, its queued record and claim go, its slot is parked when it can be.
// The branch stays for salvage. Steps already done are skipped, so a repeated
// call finishes the rest.
func (e Env) Pick(ctx context.Context, root string, be Board, o PickOpts) (res Picked, err error) {
	reason := strings.TrimSpace(o.Reason)
	if reason == "" {
		return res, usage("--reason-file is empty: say why")
	}
	ok, err := e.holdsLease(ctx, root, o.HolderPID)
	if err != nil {
		return res, wrap(err)
	}
	if !ok {
		return res, blocked(BlockNoRound, "this process holds no round lease: run rota round start first")
	}
	reg, err := worker.LoadRegistry(root)
	if err != nil {
		return res, err
	}
	rec := reg.BestOf(o.ID)
	if rec == nil {
		return res, blocked(BlockNotBestOf, "#%s has no best-of attempts in this round's registry", strings.TrimPrefix(o.ID, "#"))
	}
	id := strings.TrimPrefix(o.ID, "#")
	res.ID = id
	want, ok := worker.PRRefNumber(o.PR)
	if !ok {
		return res, usage("--pr %q is not a PR number or URL", o.PR)
	}
	var openPRs []tracker.PR
	var openLoaded bool
	forgeOpen := func() ([]tracker.PR, error) {
		if !openLoaded && e.Forge != nil {
			prs, err := e.Forge.OpenPRs(ctx)
			if err != nil {
				return nil, err
			}
			openPRs, openLoaded = prs, true
		}
		return openPRs, nil
	}
	views := make([]pickView, 0, len(rec.Attempts))
	for _, a := range rec.Attempts {
		v, err := e.resolveAttempt(root, reg, id, a, forgeOpen)
		if err != nil {
			return res, wrap(err)
		}
		views = append(views, v)
	}
	winner, loser := -1, -1
	for i, v := range views {
		if n, ok := v.number(); ok && n == want {
			winner = i
		}
	}
	if rec.Pick != "" && !rec.Picked(o.PR) {
		return res, blocked(BlockPicked, "#%s already picked %s", id, rec.Pick)
	}
	if winner < 0 && rec.Pick != "" { // the picked PR no longer resolves (merged, say)
		res.Winner = PickAttempt{PR: fmt.Sprintf("#%d", want)}
		return res, nil
	}
	if winner < 0 {
		var have []string
		for _, v := range views {
			have = append(have, fmt.Sprintf("%s: %s", v.Slot, firstNonEmpty(v.PR, v.State)))
		}
		return res, blocked(BlockNotAttempt, "#%d is not an attempt of #%s (attempts: %s)", want, id, strings.Join(have, ", "))
	}
	for i := range views {
		if i != winner {
			loser = i
		}
	}
	res.Winner = views[winner].PickAttempt
	var lv pickView
	if loser >= 0 {
		lv = views[loser]
		res.Loser = lv.PickAttempt
	}
	if rec.Pick != "" {
		return res, nil
	}
	if lv.State == AttemptNoPR && lv.slot != nil {
		if st := lv.slot.State(); st == "busy" || st == "" || st == "blocked" {
			return res, blocked(BlockAttemptRunning, "%s is still building #%s: wait for its PR, or reclaim it", lv.Slot, id)
		}
	}

	// The loser's PR: found before anything changes, so a forge that cannot
	// close refuses cleanly.
	var closer prCloser
	loserN, hasPR := lv.number()
	toClose := false
	if hasPR && lv.State == AttemptOpenPR {
		toClose = true
		if e.Forge != nil {
			if st, err := e.Forge.PRState(ctx, loserN); err == nil && (st == "closed" || st == "merged") {
				toClose = false
			}
		}
		if toClose {
			c, isCloser := e.Forge.(prCloser)
			if !isCloser {
				return res, unavailable("closing #%d needs a forge that can close a PR; %s", loserN, firstNonEmpty(e.ForgeErr, "this one cannot"))
			}
			closer = c
		}
	}
	if toClose {
		comment := fmt.Sprintf("Closed by `rota round pick`: #%s is best-of:2 and the orchestrator picked #%d (slot %s).\n\nReason:\n%s\n\nBranch `%s` stays for salvage until `rota reap`.",
			id, want, res.Winner.Slot, reason, lv.Branch)
		if err := closer.PRClose(ctx, loserN, comment); err != nil {
			return res, wrap(err)
		}
		res.Closed = true
	}
	if loser >= 0 {
		if hasPR {
			for _, q := range reg.PRs() {
				if n, ok := worker.PRRefNumber(q.PR); ok && n == loserN && strings.EqualFold(queuedIssue(q), id) {
					if err := worker.RemoveQueuedPR(root, q.PR); err != nil {
						return res, wrap(err)
					}
				}
			}
		}
		tolerate := tolerateMissing(&res.Warnings, id)
		if _, err := releaseClaims(be, id, lv.Slot, lv.claimID, false); tolerate("claim release", err) != nil {
			return res, wrap(err)
		}
		if lv.slot != nil {
			if err := e.retireSlot(ctx, root, lv, id, &res.Warnings); err != nil {
				return res, wrap(err)
			}
		}
	}
	if err := worker.SetBestOfPick(root, id, fmt.Sprintf("#%d", want)); err != nil {
		return res, wrap(err)
	}
	if err := worker.ClearBounces(root, o.ID); err != nil {
		return res, wrap(err)
	}
	worker.LedgerNote(root, ledger.Entry{Kind: ledger.KindPick, Issue: id, Slot: res.Winner.Slot, PR: fmt.Sprintf("#%d", want),
		Detail: ledger.Detail("loser", lv.Slot)})
	note := fmt.Sprintf("Best-of pick: #%d (%s)", want, res.Winner.Slot)
	if loser >= 0 {
		note += fmt.Sprintf(" over %s (%s)", firstNonEmpty(lv.PR, "the other attempt"), lv.Slot)
		if !hasPR {
			note += ", which had no PR"
		}
	}
	first, _, _ := strings.Cut(reason, "\n")
	note += ". Reason: " + strings.TrimSpace(first)
	if _, err := be.AddComment(id, "feedback", note); err != nil {
		return res, wrap(err)
	}
	res.Changed = true
	return res, nil
}

// resolveAttempt finds an attempt's PR: the PR its slot records while it still
// holds the issue on that branch, else a queued record of the issue from that
// slot or branch, else an open forge PR from the branch.
func (e Env) resolveAttempt(root string, reg worker.Registry, id string, a worker.BestOfAttempt, forgeOpen func() ([]tracker.PR, error)) (pickView, error) {
	v := pickView{PickAttempt: PickAttempt{Slot: a.Slot, Branch: a.Branch}, claimID: a.ClaimID}
	if s := reg.Slot(a.Slot); s != nil && strings.EqualFold(s.HeldID(), id) && s.Branch() == a.Branch {
		v.slot = s
		v.claimID = firstNonEmpty(a.ClaimID, s.ClaimID())
		if n, ok := worker.PRRefNumber(s.PR()); ok {
			v.PR, v.State = fmt.Sprintf("#%d", n), AttemptOpenPR
			return v, nil
		}
	}
	for _, q := range reg.PRs() {
		if !strings.EqualFold(queuedIssue(q), id) || (q.From != a.Slot && q.Branch != a.Branch) {
			continue
		}
		if n, ok := worker.PRRefNumber(q.PR); ok {
			v.PR, v.State = fmt.Sprintf("#%d", n), AttemptOpenPR
			v.claimID = firstNonEmpty(v.claimID, q.ClaimID)
			return v, nil
		}
	}
	prs, err := forgeOpen()
	if err != nil {
		return v, err
	}
	for _, p := range prs {
		if p.Branch == a.Branch {
			v.PR, v.State = fmt.Sprintf("#%d", p.Number), AttemptOpenPR
			return v, nil
		}
	}
	v.State = AttemptGone
	if v.slot != nil {
		v.State = AttemptNoPR
	}
	return v, nil
}

// retireSlot frees the loser's slot when its worker is finished and the
// worktree is clean (the branch stays); any other slot is left for reclaim.
func (e Env) retireSlot(ctx context.Context, root string, v pickView, id string, warnings *[]string) error {
	st := v.slot.State()
	if v.slot.IsExternal() { // an adopted checkout is never parked
		*warnings = append(*warnings, fmt.Sprintf("slot %s is an adopted external slot holding #%s: release it with rota worker pool reap %s", v.Slot, id, v.Slot))
		return nil
	}
	if st == "done" || st == "idle" {
		if dirty, err := e.dirtyPaths(ctx, v.slot.Worktree()); err == nil && len(dirty) == 0 {
			if _, err := e.Park(ctx, root, v.Slot, "pick"); err != nil {
				return err
			}
			return freeSlot(root, v.Slot, false)
		}
		st = "uncommitted changes"
	}
	*warnings = append(*warnings, fmt.Sprintf("slot %s still holds #%s (%s): free it with rota round reclaim %s", v.Slot, id, firstNonEmpty(st, "busy"), v.Slot))
	return nil
}
