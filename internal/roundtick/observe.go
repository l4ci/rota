package roundtick

import (
	"context"
	"strings"

	"github.com/l4ci/rota/internal/round"
)

// Inspection is one reconcile step. Execution fills Repaired; observation
// fills Proposed, which describes repair attempts without claiming success.
type Inspection struct {
	Repaired, Proposed, Drift []Finding
	Unknown                   []Item
}

// InspectionOf reads a round.Outcome from either world. A read-only world
// keeps every finding in Drift, repairable ones included: a proposed repair is
// reported as a proposal, not also as drift the autopilot does not repair.
func InspectionOf(out round.Outcome) Inspection {
	var in Inspection
	proposed := map[string]bool{}
	for _, f := range out.Proposed {
		f := FindingOf(f)
		in.Proposed = append(in.Proposed, f)
		proposed[f.String()] = true
	}
	for _, f := range out.Repaired {
		in.Repaired = append(in.Repaired, FindingOf(f))
	}
	for _, f := range out.Drift {
		if f := FindingOf(f); !proposed[f.String()] {
			in.Drift = append(in.Drift, f)
		}
	}
	if out.Report != nil {
		for _, source := range out.Report.Unavailable {
			in.Unknown = append(in.Unknown, Item{Kind: "source", Target: source, Why: "reconciliation source unavailable; dependent repairs are not verified"})
		}
		for _, warning := range out.Report.Warnings {
			in.Unknown = append(in.Unknown, Item{Kind: "reconcile", Target: "round", Why: warning})
		}
	}
	return in
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
	r.propose(Action{Action: action, Target: strings.Join(targets, ","), Detail: "eligible to attempt landing into " + base}, "current state, reviews, manual gates, verification and merge result")
	if e.Observe {
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
