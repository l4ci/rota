// Package postgate is the item bookkeeping that follows a merge gate or a merge
// train: a landed PR ends its item's clocks and counts, a PR the gate sends
// back to its worker counts one bounce, and an item bounced round.maxBounces
// times is parked for a human. Gate and train share one rule here, so the verbs
// and the round autopilot cannot drift apart.
package postgate

import (
	"errors"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/worker"
)

// Park sends an item to a human: the move `round transfer --to human` makes.
// note is the comment it leaves on the item.
type Park func(issue, note string, set roundcfg.Settings) error

// Outcome is what the bookkeeping did for one item. Bounces is the item's
// count after a bounce (0 when the verdict was no bounce), Parked whether the
// count reached round.maxBounces and the item was parked.
type Outcome struct {
	Bounces int
	Parked  bool
}

// Failure is a bookkeeping step that failed for one target. The verdict stands;
// the caller reports it.
type Failure struct {
	Target string
	Err    error
}

// Gate does the per-item bounce accounting after a real gate run (never
// --check-only: that would count a peek). A pass forgets the item's count and
// ends its clocks. A stale or provenance-fail verdict sends the PR back to its
// worker: it counts, and at round.maxBounces the item is parked instead (the PR
// stays open) so it is never re-dispatched silently.
func Gate(env worker.Env, root, issue string, r worker.GateResult, park Park) (Outcome, error) {
	if issue == "" {
		return Outcome{}, nil
	}
	switch m := worker.ClassifyVerdict(r.Verdict); {
	case r.OK():
		return Outcome{}, landed(env, root, issue)
	case m.Bounce:
		return bounce(env, root, issue, r, park)
	}
	return Outcome{}, nil
}

// Train does the same accounting after a merge train, for every member it
// names. issues maps a train target to its item; a target with none (a slot
// without one) is skipped. A landed member ends its item's clocks and count. A
// culprit whose verdict bounces counts like a gate bounce.
func Train(env worker.Env, round *worker.RoundMemo, root string, issues map[string]string, r worker.TrainResult, park Park) []Failure {
	var fails []Failure
	for _, m := range r.Members {
		if issue := issues[m.Target]; m.Landed && issue != "" {
			if err := landed(env, root, issue); err != nil {
				fails = append(fails, Failure{m.Target, err})
			}
		}
	}
	if issue := issues[r.Culprit]; issue != "" && worker.ClassifyVerdict(r.Verdict).Bounce {
		g := r.CulpritGate
		g.Slot, g.Verdict, g.Err, g.Round = r.Culprit, r.Verdict, r.Err, round
		if _, err := bounce(env, root, issue, g, park); err != nil {
			fails = append(fails, Failure{r.Culprit, err})
		}
	}
	return fails
}

// landed ends an item whose PR landed: its wall-clock (round.itemTimeoutMinutes),
// its best-of record and its bounce count.
func landed(env worker.Env, root, issue string) error {
	if err := worker.ClearItemStart(root, issue); err != nil {
		return err
	}
	if err := worker.ClearBestOf(root, issue); err != nil {
		return err
	}
	store, err := env.BounceStore(root)
	if err != nil {
		return err
	}
	return store.Clear(issue)
}

// bounce counts one bounce for the item and parks it at round.maxBounces. A
// failure to park is returned, the count stands.
func bounce(env worker.Env, root, issue string, r worker.GateResult, park Park) (Outcome, error) {
	// r.Slot echoes the gate argument, a PR number after its slot moved on; the
	// slot the target resolved to names the ledger row.
	set, err := roundcfg.FromConfig(env.WithDefaults().Config(root))
	if err != nil {
		return Outcome{}, err
	}
	store, err := env.BounceStore(root)
	if err != nil {
		return Outcome{}, err
	}
	rec, err := store.Record(r.Round, issue, r.TargetSlot, r.SHA, set.MaxBounces)
	if err != nil {
		return Outcome{}, err
	}
	n := rec.N
	if set.MaxBounces == 0 || n < set.MaxBounces {
		// A support machine's worker learns of the bounce on its PR (#1598).
		// The post is idempotent per head, so it also goes out for a head
		// already counted: a failed post whose undo failed too still lands on
		// the retry.
		if _, err := env.PostReviewBounce(root, r.Branch, r.PR, r.SHA, r.Err); err != nil {
			if rec.Counted {
				n--
			}
			return Outcome{Bounces: n}, errors.Join(err, rec.Undo())
		}
		return Outcome{Bounces: n}, nil
	}
	why, _, _ := strings.Cut(r.Err, "\n")
	what := r.PR
	if what == "" {
		what = "branch " + r.Branch
	}
	note := fmt.Sprintf("The merge gate sent %s back %d time(s) (round.maxBounces is %d), so it is parked for a human instead of going back to a worker. Last refusal: %s", what, n, set.MaxBounces, why)
	if err := park(issue, note, set); err != nil {
		return Outcome{Bounces: n}, fmt.Errorf("could not park %s as needs-human: %w", issue, err)
	}
	return Outcome{Bounces: n, Parked: true}, store.Clear(issue)
}
