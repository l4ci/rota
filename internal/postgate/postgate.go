// Package postgate is the item bookkeeping that follows a merge gate or a merge
// train: a landed PR ends its item's clocks and bounce count, and a PR the gate
// sends back to its worker (a stale merge, a provenance failure) is told on the
// PR without counting. No gate or train verdict counts a bounce; review bounces
// are counted by the round review verbs. Gate and train share one rule here, so
// the verbs and the round autopilot cannot drift apart.
package postgate

import (
	"context"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/worker"
)

// Book is the item bookkeeping a landing leaves behind. Run, Gate and Train
// share this port; WorkerBook is the production adapter.
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
	// Targets resolves the gate targets a train names, by the argument given.
	// A target that does not resolve is left out; an unreadable registry is an
	// error.
	Targets(root string, args []string) (map[string]worker.GateTarget, error)
	// ReleaseSlice drops the claim slot holds on issue and its in-progress label
	// when the landed PR left the issue open for later slices (#3629). It is a
	// no-op for any other issue.
	ReleaseSlice(root, issue, slot string) error
	// HasOrigin reports whether the repo at root has an origin remote.
	HasOrigin(ctx context.Context, root string) bool
}

var _ Book = WorkerBook{}

// WorkerBook adapts a worker.Env to Book. Release is the tracker side of
// ReleaseSlice; without it a slice's claim is left for reconcile to repair.
type WorkerBook struct {
	Env     worker.Env
	Release func(root, issue, slot string) error
}

func (b WorkerBook) ReleaseSlice(root, issue, slot string) error {
	if b.Release == nil || slot == "" {
		return nil
	}
	return b.Release(root, issue, slot)
}

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

func (b WorkerBook) Targets(root string, args []string) (map[string]worker.GateTarget, error) {
	return worker.ResolveGateTargets(root, args)
}

func (b WorkerBook) HasOrigin(ctx context.Context, root string) bool {
	res, err := git.Repo{Dir: root, Runner: b.Env.Git}.Run(ctx, "remote", "get-url", "origin")
	return err == nil && res.ExitCode == 0
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
//
// A landed integration PR (#3096) ends every member issue it carried, as a
// train ends each member it landed.
func Gate(book Book, root, issue string, r worker.GateResult) error {
	// already-merged is a retry after the forge merged it but a later step failed.
	if (r.OK() || r.Verdict == worker.GateAlreadyMerged) && len(r.IntegrationMembers) > 0 {
		for _, m := range r.IntegrationMembers {
			if err := landed(book, root, m, r.Slot); err != nil {
				return err
			}
		}
		return nil
	}
	if issue == "" {
		return nil
	}
	switch m := worker.ClassifyVerdict(r.Verdict); {
	case r.OK():
		return landed(book, root, issue, r.Slot)
	case m.Notify:
		return notify(book, root, r)
	}
	return nil
}

// Train does the same bookkeeping after a merge train, for every member it
// names. issues maps a train target to its item; a target with none (a slot
// without one) is skipped. targets supplies the culprit's branch and PR when
// the result carries neither (a failure before any member gate ran), so Train
// notifies the right PR without Run having patched the result first. A landed member ends its item's clocks and count. A
// culprit whose verdict notifies is told on its PR.
func Train(book Book, round *worker.RoundMemo, root string, issues map[string]string, targets map[string]worker.GateTarget, r worker.TrainResult) []Failure {
	var fails []Failure
	for _, m := range r.Members {
		if issue := issues[m.Target]; m.Landed && issue != "" {
			if err := landed(book, root, issue, targets[m.Target].Name); err != nil {
				fails = append(fails, Failure{m.Target, err})
			}
		}
	}
	if issue := issues[r.Culprit]; issue != "" && worker.ClassifyVerdict(r.Verdict).Notify {
		g := trainCulprit(issues, targets, r)
		g.Slot, g.Round = r.Culprit, round
		if err := notify(book, root, g); err != nil {
			fails = append(fails, Failure{r.Culprit, err})
		}
	}
	return fails
}

// landed ends an item whose PR landed: its wall-clock (round.itemTimeoutMinutes),
// its best-of record and its bounce count. A slice of a partial-slice issue
// also gives up its claim, so the next slice is assignable to any slot.
func landed(book Book, root, issue, slot string) error {
	if err := book.ClearItemStart(root, issue); err != nil {
		return err
	}
	if err := book.ClearBestOf(root, issue); err != nil {
		return err
	}
	if err := book.ClearBounces(root, issue); err != nil {
		return err
	}
	return book.ReleaseSlice(root, issue, slot)
}

// notify tells a support machine's worker its PR was sent back, with no count
// and no park. The post is idempotent per head, so a tick that gates the same
// head again posts nothing new.
func notify(book Book, root string, r worker.GateResult) error {
	return book.PostReviewBounce(root, r.Branch, r.PR, r.SHA, r.Err)
}
