package round

import (
	"context"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"strings"

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
// it holds an issue, records a PR, reports done or idle and its worktree has
// no dirty paths. why names what is missing when it is not.
func (e Env) parkable(ctx context.Context, s *jsonx.Object) (ok bool, why string) {
	switch {
	case slotIssue(s) == "":
		return false, "holds nothing"
	case worker.Str(s, "pr") == "":
		return false, "no PR recorded"
	}
	if st := worker.Str(s, "state"); st != "done" && st != "idle" {
		return false, firstNonEmpty(st, "busy")
	}
	dirty, err := e.dirtyPaths(ctx, worker.Str(s, "worktree"))
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
func (e Env) queuePR(ctx context.Context, root, name string) error {
	reg := worker.LoadRegistry(root)
	s := reg.Slot(name)
	if s == nil {
		return &exitcode.Error{Exit: exitcode.ExitResolution, Message: fmt.Sprintf("slot %s is not in the pool", name)}
	}
	if ok, why := e.parkable(ctx, s); !ok {
		return blocked(BlockSlotBusy, "%s", busyMsg(name, slotIssue(s), why))
	}
	p, err := e.Park(ctx, root, name, "assign")
	if err != nil {
		return err
	}
	pr := worker.Str(s, "pr")
	merged := false
	if n, ok := prNumber(pr); ok && e.Forge != nil {
		if st, err := e.Forge.PRState(ctx, n); err == nil && st == "merged" {
			merged = true
		}
	}
	rec := jsonx.NewObject()
	rec.Set("issue", slotIssue(s))
	rec.Set("branch", firstNonEmpty(p.Branch, worker.Str(s, "branch")))
	rec.Set("pr", pr)
	rec.Set("base", firstNonEmpty(worker.Str(s, "base"), e.Base))
	rec.Set("from", name)
	rec.Set("claimId", worker.Str(s, "claimId"))
	rec.Set("round", registryRound(root))
	relays, _ := s.Get("relays")
	if relays == nil {
		relays = []any{}
	}
	rec.Set("relays", relays)
	return worker.Update(root, slotsDefault(), func(doc *jsonx.Object) {
		if !merged {
			worker.QueuePR(doc, rec)
		}
		if cur := (worker.Registry{Doc: doc}).Slot(name); cur != nil {
			cur.Set("task", nil)
			cur.Set("pr", nil)
			cur.Delete("claimId")
			cur.Set("state", "idle")
			cur.Set("branch", "park/"+name)
		}
	})
}
