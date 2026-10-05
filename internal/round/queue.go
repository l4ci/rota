package round

import (
	"context"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/worker"
)

// The PR queue: a slot whose worker is done and whose PR is open is free for
// the next issue. Its PR waits in the registry's `prs` (review and merge do not
// need the slot), and the issue stays claimed and in progress.

// queuedIssue is the issue a queued record holds, in the backend's spelling.
func queuedIssue(q *jsonx.Object) string {
	return strings.ToUpper(strings.TrimPrefix(strings.TrimSpace(worker.Str(q, "issue")), "#"))
}

// parkable says whether a slot's PR can be queued so the slot takes new work:
// it holds an issue, records a PR (or, for a review item, the issues it filed),
// reports done or idle and its worktree has
// no dirty paths. why names what is missing when it is not.
func (e Env) parkable(ctx context.Context, s *worker.Slot) (ok bool, why string) {
	switch {
	case slotIssue(s) == "":
		return false, "holds nothing"
	case s.PR() == "" && len(s.Issues()) == 0:
		return false, "no PR recorded"
	}
	if st := s.State(); st != "done" && st != "idle" {
		return false, firstNonEmpty(st, "busy")
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
	reg := worker.LoadRegistry(root)
	s := reg.Slot(name)
	if s == nil {
		return &exitcode.Error{Exit: exitcode.ExitResolution, Message: fmt.Sprintf("slot %s is not in the pool", name)}
	}
	if ok, why := e.parkable(ctx, s); !ok {
		return blocked(BlockSlotBusy, "%s", busyMsg(name, slotIssue(s), why))
	}
	if s.PR() == "" {
		return e.closeReview(ctx, root, be, s)
	}
	p, err := e.Park(ctx, root, name, "assign")
	if err != nil {
		return err
	}
	pr := s.PR()
	merged := false
	if n, ok := prNumber(pr); ok && e.Forge != nil {
		if st, err := e.Forge.PRState(ctx, n); err == nil && st == "merged" {
			merged = true
		}
	}
	rec := jsonx.NewObject()
	rec.Set("issue", slotIssue(s))
	rec.Set("branch", firstNonEmpty(p.Branch, s.Branch()))
	rec.Set("pr", pr)
	rec.Set("base", firstNonEmpty(s.Base(), e.Base))
	rec.Set("from", name)
	rec.Set("claimId", s.ClaimID())
	rec.Set("round", registryRound(root))
	relays := s.Relays()
	if relays == nil {
		relays = []any{}
	}
	rec.Set("relays", relays)
	return worker.UpdateDoc(root, func(doc *jsonx.Object) {
		if !merged {
			worker.QueuePR(doc, rec)
		}
		if cur := (worker.Registry{Doc: doc}).Slot(name); cur != nil {
			cur.Park(false)
		}
	})
}

// closeReview frees a slot whose architecture-review item reported done with
// the issues it filed (`ROTA-DONE <slot> issues:#a,#b`): the item is closed
// with a note listing them and the slot is freed. Only a review item this
// round minted closes this way, so a worker cannot close an arbitrary issue by
// naming issues in its done line; any other held item is refused.
func (e Env) closeReview(ctx context.Context, root string, be Board, s *worker.Slot) error {
	name, id := s.Name(), slotIssue(s)
	it, err := be.Get(id)
	if err != nil {
		return wrap(err)
	}
	if !IsReviewTitle(it.Title) || !MintedReviews(root)[strings.ToUpper(it.ID)] {
		return blocked(BlockSlotBusy, "%s", busyMsg(name, id, "reported issues, but it is not an architecture review item; it needs a PR"))
	}
	if _, err := e.Park(ctx, root, name, "assign"); err != nil {
		return err
	}
	now := time.Now
	if e.Now != nil {
		now = e.Now
	}
	note := "filed " + strings.Join(s.Issues(), ", ")
	// The close note is dropped by the file backend, so the list also goes in a comment.
	if _, err := be.AddComment(it.ID, "feedback", "Review done: "+note+"."); err != nil {
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
