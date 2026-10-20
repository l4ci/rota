package round

import (
	"context"
	"strings"

	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// BlockNoSlotHolds: accept found no slot holding the item, and the item is not
// one it already closed.
const BlockNoSlotHolds = "no slot holds it"

// BlockNoIssues: neither the slot nor --issues names the filed issues.
const BlockNoIssues = "no issues"

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
	// Changed is false when the item was already closed by an earlier accept.
	Changed bool
}

// Accept is the orchestrator's close for an item that delivers no PR. It finds
// the slot holding the round-minted architecture review, takes the filed issues
// from the slot or from in.Issues, and closes the item and frees the slot
// through closeNoPR. An item it already closed answers success unchanged, so a
// lost response can be retried.
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
		return res, blocked(BlockNoSlotHolds, "no slot holds %s", tracker.RefOf(id))
	}
	res.Slot = s.Name()
	it, err := reviewItem(root, be, s.Name(), id, "accept needs an architecture review")
	if err != nil {
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
	if err := e.closeNoPR(ctx, root, be, s, it, note); err != nil {
		return res, err
	}
	res.Issues, res.Changed = issues, true
	return res, nil
}
