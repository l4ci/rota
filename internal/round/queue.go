package round

import (
	"context"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/strutil"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// The PR queue: a slot whose worker is done and whose PR is open is free for
// the next issue. Its PR waits in the registry's `prs` (review and merge do not
// need the slot), and the issue stays claimed and in progress.

// queuedIssue is the issue a queued record holds, in the backend's spelling.
func queuedIssue(q worker.QueuedPR) string {
	return worker.NormID(q.Issue)
}

// parkable says whether a slot's PR can be queued so the slot takes new work:
// it holds an issue, records a PR (or, for a review item, the issues it filed),
// reports done or idle and its worktree has
// no dirty paths. why names what is missing when it is not.
func (e Env) parkable(ctx context.Context, s *worker.Slot) (ok bool, why string) {
	switch {
	case s.HeldID() == "":
		return false, "holds nothing"
	case s.PR() == "" && len(s.Issues()) == 0 && s.Finding() != "":
		return false, "its finding awaits `rota round accept`"
	case s.PR() == "" && len(s.Issues()) == 0:
		return false, fmt.Sprintf("no PR recorded for branch %s; check the forge and run `rota round reconcile --apply`", strutil.FirstNonEmpty(s.Branch(), "(none)"))
	}
	// A relay can leave a slot blocked after its PR merged: the merged PR frees it.
	if worker.IsBlocked(s.State()) && e.prMerged(ctx, s.PR()) {
		return e.settled(ctx, s, worker.IsBlocked)
	}
	return e.settled(ctx, s, worker.Parkable)
}

// settled is the slot half of parkable: no partial or failed report, a state
// the predicate accepts and a clean worktree. why names what is missing when it is not.
func (e Env) settled(ctx context.Context, s *worker.Slot, accept func(state string) bool) (ok bool, why string) {
	// A partial or failed report waits for a relay or transfer: queuing its PR
	// would let the gate merge it by number with nobody having answered it.
	if k, _ := s.Outcome(); worker.OutcomeOpen(k) {
		return false, "reported " + k + "; relay or transfer it first"
	}
	if st := s.State(); !accept(st) {
		return false, strutil.FirstNonEmpty(st, "busy")
	}
	dirty, err := e.dirtyPaths(ctx, s.Worktree())
	if err != nil {
		return false, "worktree unreadable"
	}
	if len(dirty) > 0 {
		return false, "uncommitted changes"
	}
	return true, ""
}

// prMerged says whether the forge shows the PR merged. Any other answer, or none, is no.
func (e Env) prMerged(ctx context.Context, pr string) bool {
	n, ok := worker.PRRefNumber(pr)
	if !ok || e.Forge == nil {
		return false
	}
	st, err := e.Forge.PRState(ctx, n)
	return err == nil && st == "merged"
}

// adoptBranchPR is the assign-side repair for a worker whose done landed before
// its PR was recorded: a held slot that records no PR and no issues gets the
// open PR headed by its own branch, when the forge shows one. The PR is set on
// the loaded slot so the caller's parkable sees it, and written to the registry
// unless persist is false (a check-only run writes nothing). A forge that
// cannot answer, no PR or a parked branch leaves the slot as it was.
func (e Env) adoptBranchPR(ctx context.Context, root string, s *worker.Slot, persist bool) {
	if e.Forge == nil || s.HeldID() == "" || s.PR() != "" || len(s.Issues()) > 0 || s.Branch() == "" || worker.IsOwnPark(s.Branch(), s.Name()) {
		return
	}
	if !worker.Parkable(s.State()) {
		return
	}
	url := e.branchPR(ctx, root, s.Branch(), strutil.FirstNonEmpty(s.Base(), e.Base))
	if url == "" {
		return
	}
	if persist && editSlot(root, s.Name(), func(w *worker.Slot) error { w.SetPR(url); return nil }) != nil {
		return
	}
	s.SetPR(url)
}

// branchPR is the URL of the PR headed by branch: the open one, else the merged
// one the branch owns (it opened and merged between two looks at the open
// list). "" when the forge has none or cannot answer.
func (e Env) branchPR(ctx context.Context, root, branch, base string) string {
	if e.Forge == nil || branch == "" {
		return ""
	}
	if url := e.openBranchPR(ctx, branch); url != "" {
		return url
	}
	if m, _ := e.mergedBranchPR(ctx, root, branch, base); m != nil {
		return m.URL
	}
	return ""
}

// openBranchPR is the URL of the open PR headed by branch, "" when none.
func (e Env) openBranchPR(ctx context.Context, branch string) string {
	prs, err := e.Forge.OpenPRs(ctx)
	if err != nil {
		return ""
	}
	for _, p := range prs {
		if p.Branch == branch && p.URL != "" {
			return p.URL
		}
	}
	return ""
}

// mergedBranchPR is the merged PR headed by branch that the branch owns, nil
// when none. The error is the forge's: callers that act on the answer report it.
func (e Env) mergedBranchPR(ctx context.Context, root, branch, base string) (*tracker.PR, error) {
	merged, err := e.Forge.MergedPRs(ctx, branch)
	if err != nil {
		return nil, err
	}
	for i := range merged {
		if merged[i].URL != "" && e.ownsMergedPR(ctx, root, merged[i].HeadSHA, base, branch) {
			return &merged[i], nil
		}
	}
	return nil, nil
}

// busyMsg is the refusal text for a slot that holds an issue and cannot park.
func busyMsg(name, held, why string) string {
	return fmt.Sprintf("slot %s holds %s (%s; a slot frees when its worker reports done with a PR)", name, held, why)
}

// queuePR frees a parkable slot by queuing its PR: the branch is pushed and the
// worktree moves to park/<agent> (no salvage commit, it has no dirty paths),
// then in one registry write the record is appended and the slot freed like
// freeSlot does. A PR the forge already shows merged leaves no record, the work
// is in; any other forge answer, or none, keeps it. The issue's claim and
// in-progress state stay: it is still taken. No handoff comment.
func (e Env) queuePR(ctx context.Context, root string, be Board, name string) error {
	reg, err := worker.LoadRegistry(root)
	if err != nil {
		return err
	}
	s := reg.Slot(name)
	if s == nil {
		return &exitcode.Error{Exit: exitcode.ExitResolution, Message: fmt.Sprintf("slot %s is not in the pool", name)}
	}
	if ok, why := e.parkable(ctx, s); !ok {
		return blocked(BlockSlotBusy, "%s", busyMsg(name, s.HeldID(), why))
	}
	if s.PR() == "" {
		return e.closeReview(ctx, root, be, s)
	}
	// A PR another issue's record already holds is not this slot's: the pane's
	// last ROTA-DONE is the previous issue's until the new worker prints its own.
	// Queuing it would pair that PR with this issue and branch.
	if q := reg.QueuedPR(s.PR()); q != nil && queuedIssue(*q) != s.HeldID() {
		return blocked(BlockSlotBusy, "%s", busyMsg(name, s.HeldID(), fmt.Sprintf("its recorded PR %s is already queued for %s", s.PR(), q.Issue)))
	}
	p, err := e.Park(ctx, root, name, "assign")
	if err != nil {
		return err
	}
	pr := s.PR()
	merged := e.prMerged(ctx, pr)
	rec := worker.QueuedPR{
		Issue: s.HeldID(), Branch: strutil.FirstNonEmpty(p.Branch, s.Branch()), PR: pr,
		Base: strutil.FirstNonEmpty(s.Base(), e.Base), From: name, ClaimID: s.ClaimID(),
		Round: registryRound(root), Relays: s.Relays(), Bundle: s.Bundle(),
	}
	return worker.Update(root, func(doc *worker.Doc) {
		if !merged {
			doc.QueuePR(rec)
		}
		if cur := doc.Slot(name); cur != nil {
			cur.Park(false)
		}
	})
}

// reviewItem returns the item a slot holds when it is an architecture-review
// item this round minted, and a "needs a PR" refusal otherwise. Only such an
// item closes without a PR, so a worker cannot close an arbitrary issue by
// naming issues in its done line.
func reviewItem(root string, be Board, name, id, did string) (*backlog.Item, error) {
	it, err := be.Get(id)
	if err != nil {
		return it, wrap(err)
	}
	if !IsReviewTitle(it.Title) || !MintedReviews(root)[worker.NormID(it.ID)] {
		return it, blocked(BlockSlotBusy, "%s", busyMsg(name, id, did+", but it is not an architecture review item; it needs a PR"))
	}
	return it, nil
}

// closeReview frees a slot whose architecture-review item reported done with
// the issues it filed (`ROTA-DONE <slot> issues:#a,#b`): the item is closed
// with a note listing them and the slot is freed. Any other held item is refused.
func (e Env) closeReview(ctx context.Context, root string, be Board, s *worker.Slot) error {
	it, err := reviewItem(root, be, s.Name(), s.HeldID(), "reported issues")
	if err != nil {
		return err
	}
	return e.closeNoPR(ctx, root, be, s, it, "Review done: ", "filed "+strings.Join(s.Issues(), ", "))
}

// closeNoPR completes an item that delivers no PR: the slot parks, the note is
// commented on the item and closes it without proof, the claim and in-progress
// state go and the slot is freed. Assign, transfer and accept all close here;
// did opens the comment ("Review done: ", "Finding accepted: ").
func (e Env) closeNoPR(ctx context.Context, root string, be Board, s *worker.Slot, it *backlog.Item, did, note string) error {
	name := s.Name()
	if _, err := e.Park(ctx, root, name, "assign"); err != nil {
		return err
	}
	now := e.Worker.WithDefaults().Now
	// The close note is dropped by the file backend, so it also goes in a comment.
	if _, err := be.AddComment(it.ID, "feedback", did+note+"."); err != nil {
		return wrap(err)
	}
	if _, err := be.Complete(it.ID, backlog.CompleteInput{Date: now().Format("2006-01-02"), Reason: "done", Note: note, NoProof: true}); err != nil {
		return wrap(err)
	}
	if _, err := releaseClaims(be, it.ID, name, s.ClaimID(), true); err != nil {
		return wrap(err)
	}
	if _, err := be.SetState(it.ID, "none"); err != nil {
		return wrap(err)
	}
	return freeSlot(root, name, false)
}
