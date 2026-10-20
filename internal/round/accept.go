package round

import (
	"context"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// BlockNoSlotHolds: accept found no slot holding the item, and the item is not
// one it already closed.
const BlockNoSlotHolds = "no slot holds it"

// BlockNoIssues: neither the slot nor --issues names the filed issues.
const BlockNoIssues = "no issues"

// BlockFindingMissing: a research item has no finding comment posted since its
// assignment.
const BlockFindingMissing = "finding missing"

// BlockNoGist: a decision item closes with a one-line gist for the milestone's
// decision log.
const BlockNoGist = "gist missing"

// AcceptInput is the flags of `rota round accept`.
type AcceptInput struct {
	ID string
	// Issues supplies the filed issues when the slot recorded none.
	Issues []string
	// Gist replaces the close note ("filed #a, #b").
	Gist string
}

// Accepted is what Accept did.
type Accepted struct {
	ID, Slot string
	Issues   []string
	// Finding is the comment id a research worker reported, "" for a review.
	Finding string
	// Changed is false when the item was already closed by an earlier accept.
	Changed bool
}

// Accept is the orchestrator's close for an item that delivers no PR. It finds
// the slot holding the item: a round-minted architecture review closes on the
// filed issues from the slot or from in.Issues, a decision:research item on the
// finding its worker posted and a gist. Either way the item closes and the slot
// frees through closeNoPR. An item it already closed answers success unchanged,
// so a lost response can be retried.
func (e Env) Accept(ctx context.Context, root string, be Board, in AcceptInput) (Accepted, error) {
	id := worker.NormID(in.ID)
	res := Accepted{ID: id}
	reg, err := worker.LoadRegistry(root)
	if err != nil {
		return res, err
	}
	var s *worker.Slot
	for _, c := range reg.Slots() {
		if c.HeldID() == id {
			s = c
			break
		}
	}
	if s == nil {
		it, err := be.Get(id)
		if err != nil {
			return res, wrap(err)
		}
		if it.Closed && IsReviewTitle(it.Title) && MintedReviews(root)[worker.NormID(it.ID)] {
			return res, nil
		}
		if d, _ := DecisionOf(be.Capabilities(), *it); it.Closed && d == DecisionResearch {
			return res, nil
		}
		return res, blocked(BlockNoSlotHolds, "no slot holds %s", tracker.RefOf(id))
	}
	res.Slot = s.Name()
	it, err := be.Get(id)
	if err != nil {
		return res, wrap(err)
	}
	dec, err := DecisionOf(be.Capabilities(), *it)
	if err != nil {
		return res, err
	}
	if dec == DecisionResearch {
		return e.acceptResearch(ctx, root, be, s, it, in, res)
	}
	if it, err = reviewItem(root, be, s.Name(), id, "accept needs an architecture review or a research item"); err != nil {
		return res, err
	}
	if ok, why := e.settled(ctx, s, "done", "idle", "blocked"); !ok {
		return res, blocked(BlockSlotBusy, "%s", busyMsg(s.Name(), id, why))
	}
	issues := s.Issues()
	if len(in.Issues) > 0 {
		issues = in.Issues
	}
	if len(issues) == 0 {
		return res, blocked(BlockNoIssues, "slot %s recorded no filed issues: pass --issues #a,#b", s.Name())
	}
	note := strings.TrimSpace(in.Gist)
	if note == "" {
		note = "filed " + strings.Join(issues, ", ")
	}
	if err := e.closeNoPR(ctx, root, be, s, it, "Review done: ", note); err != nil {
		return res, err
	}
	res.Issues, res.Changed = issues, true
	return res, nil
}

// acceptResearch closes a decision:research item whose worker posted a finding
// since the assignment. The gist is the line the milestone's decision log gets.
func (e Env) acceptResearch(ctx context.Context, root string, be Board, s *worker.Slot, it *backlog.Item, in AcceptInput, res Accepted) (Accepted, error) {
	if ok, why := e.settled(ctx, s, "done", "idle", "blocked"); !ok {
		return res, blocked(BlockSlotBusy, "%s", busyMsg(s.Name(), it.ID, why))
	}
	if !findingSinceAssign(be, it.ID) {
		return res, blocked(BlockFindingMissing, "%s has no finding comment since it was assigned to %s: the worker posts one with `rota item comment add --kind finding`", tracker.RefOf(it.ID), s.Name())
	}
	gist := strings.TrimSpace(in.Gist)
	if gist == "" {
		return res, blocked(BlockNoGist, "%s is a research item: pass --gist <one line> for the milestone's decision log", tracker.RefOf(it.ID))
	}
	// The decision log first: it is idempotent, so a failed close is retried by
	// running accept again.
	if _, err := backlog.LogDecision(be, it, backlog.DecisionRef(it.ID), gist); err != nil {
		return res, wrap(err)
	}
	if err := e.closeNoPR(ctx, root, be, s, it, "Finding accepted: ", gist); err != nil {
		return res, err
	}
	res.Finding, res.Changed = s.Finding(), true
	return res, nil
}

// findingSinceAssign reports whether the item carries a finding comment after
// the assignment's "In progress" comment, so a finding an earlier slot left
// does not count. An item with no such marker has no assignment to measure from.
func findingSinceAssign(be Board, id string) bool {
	cs, err := be.Comments(id, "")
	if err != nil {
		return false
	}
	start := 0
	for i, c := range cs {
		if c.Kind == "feedback" && strings.HasPrefix(c.Text, "In progress: agent") {
			start = i + 1
		}
	}
	for _, c := range cs[start:] {
		if c.Kind == "finding" {
			return true
		}
	}
	return false
}
