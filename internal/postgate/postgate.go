// Package postgate is the item bookkeeping that follows a merge gate or a merge
// train: a landed PR ends its item's clocks and bounce count, and a PR the gate
// sends back to its worker (a stale merge, a provenance failure) is told on the
// PR without counting. No gate or train verdict counts a bounce; review bounces
// are counted by the round review verbs. Gate and train share one rule here, so
// the verbs and the round autopilot cannot drift apart.
package postgate

import (
	"github.com/l4ci/rota/internal/worker"
)

// Book is the item bookkeeping a landing leaves behind: the operations Gate
// and Train need, so they run against an in-memory fake without a .rota tree.
// WorkerBook is the production adapter.
type Book interface {
	// ClearItemStart ends the item's wall-clock (round.itemTimeoutMinutes).
	ClearItemStart(root, issue string) error
	// ClearBestOf drops the item's best-of record.
	ClearBestOf(root, issue string) error
	// ClearBounces forgets the item's bounce count.
	ClearBounces(root, issue string) error
	// PostReviewBounce tells a support machine's worker its PR was sent back.
	// It is a no-op for any other branch.
	PostReviewBounce(root, branch, pr, head, why string) error
}

var _ Book = WorkerBook{}

// WorkerBook adapts a worker.Env to Book.
type WorkerBook struct{ Env worker.Env }

func (b WorkerBook) ClearItemStart(root, issue string) error {
	return worker.ClearItemStart(root, issue)
}
func (b WorkerBook) ClearBestOf(root, issue string) error { return worker.ClearBestOf(root, issue) }

func (b WorkerBook) ClearBounces(root, issue string) error {
	store, err := b.Env.BounceStore(root)
	if err != nil {
		return err
	}
	return store.Clear(issue)
}

func (b WorkerBook) PostReviewBounce(root, branch, pr, head, why string) error {
	_, err := b.Env.PostReviewBounce(root, branch, pr, head, why)
	return err
}

// Failure is a bookkeeping step that failed for one target. The verdict stands;
// the caller reports it.
type Failure struct {
	Target string
	Err    error
}

// Gate does the item bookkeeping after a real gate run (never --check-only:
// that would post on a peek). A pass forgets the item's bounce count and ends
// its clocks. A stale merge and a provenance failure are bookkeeping the worker
// repairs (worker.verdictMeanings): they count nothing and never park the item,
// but a support machine's worker is still told on its PR.
func Gate(book Book, root, issue string, r worker.GateResult) error {
	if issue == "" {
		return nil
	}
	switch m := worker.ClassifyVerdict(r.Verdict); {
	case r.OK():
		return landed(book, root, issue)
	case m.Notify:
		return notify(book, root, r)
	}
	return nil
}

// Train does the same bookkeeping after a merge train, for every member it
// names. issues maps a train target to its item; a target with none (a slot
// without one) is skipped. A landed member ends its item's clocks and count. A
// culprit whose verdict notifies is told on its PR.
func Train(book Book, round *worker.RoundMemo, root string, issues map[string]string, r worker.TrainResult) []Failure {
	var fails []Failure
	for _, m := range r.Members {
		if issue := issues[m.Target]; m.Landed && issue != "" {
			if err := landed(book, root, issue); err != nil {
				fails = append(fails, Failure{m.Target, err})
			}
		}
	}
	if issue := issues[r.Culprit]; issue != "" && worker.ClassifyVerdict(r.Verdict).Notify {
		g := trainCulprit(issues, r)
		g.Slot, g.Round = r.Culprit, round
		if err := notify(book, root, g); err != nil {
			fails = append(fails, Failure{r.Culprit, err})
		}
	}
	return fails
}

// landed ends an item whose PR landed: its wall-clock (round.itemTimeoutMinutes),
// its best-of record and its bounce count.
func landed(book Book, root, issue string) error {
	if err := book.ClearItemStart(root, issue); err != nil {
		return err
	}
	if err := book.ClearBestOf(root, issue); err != nil {
		return err
	}
	return book.ClearBounces(root, issue)
}

// notify tells a support machine's worker its PR was sent back, with no count
// and no park. The post is idempotent per head, so a tick that gates the same
// head again posts nothing new.
func notify(book Book, root string, r worker.GateResult) error {
	return book.PostReviewBounce(root, r.Branch, r.PR, r.SHA, r.Err)
}
