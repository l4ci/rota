package roundtick

import (
	"context"
	"strings"

	"github.com/l4ci/rota/internal/round"
)

// ObservationSources separates read-only inspection from mutation adapters.
// Missing probes are unknown, never permission to call an execution adapter.
type ObservationSources struct {
	Reconcile   func(context.Context) (Inspection, error)
	ReviewLoop  func(context.Context, Slot) ReviewOutcome
	ReviewFacts func(context.Context) (round.Architecture, bool, error)
	// AwaitingReview is Env.AwaitingReview without writes: it judges the
	// last-fetched refs and the local verdicts only.
	AwaitingReview func(context.Context, Slot, string) string
}

// Inspection describes repair attempts without claiming their success.
type Inspection struct {
	Proposed, Drift []string
	Unknown         []Item
}

// Proposal describes an attempted action, never an approval or landing proof.
// Conditional means earlier hypothetical actions or unknown checks may change
// whether this action is reached. Unchecked lists checks execution still owns.
type Proposal struct {
	Action
	Conditional bool
	Unchecked   []string
}

// propose records a in an observation; execution reports what it did instead.
func (r *Result) propose(a Action, unchecked ...string) {
	if !r.Observed {
		return
	}
	r.Proposed = append(r.Proposed, Proposal{Action: a, Conditional: (len(r.Proposed) > 0 || len(r.Unknown) > 0), Unchecked: unchecked})
}

// land is the shared action boundary after target selection and train fill.
func (e Env) land(ctx context.Context, r *Result, targets []string, base string) []Merged {
	action := "gate"
	if len(targets) > 1 {
		action = "train"
	}
	r.propose(Action{action, strings.Join(targets, ","), "eligible to attempt landing into " + base}, "current state, reviews, manual gates, verification and merge result")
	if e.Observe != nil {
		return nil
	}
	if len(targets) == 1 {
		return []Merged{e.gate(ctx, targets[0], base)}
	}
	return e.train(ctx, targets, base)
}

func candidateHold(c round.Candidate) string {
	var reasons []string
	if c.OpenPR != 0 {
		reasons = append(reasons, "open PR already resolves the item")
	}
	if c.DependsOnPR != 0 {
		reasons = append(reasons, "dependency PR has not landed")
	}
	if c.PickErr != "" {
		reasons = append(reasons, c.PickErr)
	}
	if c.BlockedBy != "" {
		reasons = append(reasons, c.BlockedBy)
	}
	for _, check := range c.Checks {
		if !check.OK {
			reasons = append(reasons, check.Name+": "+strings.Join(check.Detail, "; "))
		}
	}
	return strings.Join(reasons, "; ")
}
