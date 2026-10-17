package roundtick

import (
	"strings"

	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/worker"
)

// The mappings below turn the real sources (registry, reconcile, assign) into
// the plain values Env carries. The CLI hands each its source; the rules live
// here so they are testable without a Ctx.

// SlotsOf maps registry slots to the tick's roster rows.
func SlotsOf(slots []*worker.Slot) []Slot {
	var out []Slot
	for _, s := range slots {
		out = append(out, Slot{
			Name: s.Name(), State: strings.ToLower(s.State()),
			Issue: s.HeldID(),
			PR:    s.PR(),
		})
	}
	return out
}

// QueuedRefs lists the PR refs of the queued records that carry one.
func QueuedRefs(prs []worker.QueuedPR) []string {
	var out []string
	for _, q := range prs {
		if q.PR != "" {
			out = append(out, q.PR)
		}
	}
	return out
}

// ReconcileLines renders a reconcile outcome as what it repaired and the drift
// it left, one "kind key" string per finding.
func ReconcileLines(out round.Outcome) (repaired, drift []string) {
	for _, f := range out.Repaired {
		repaired = append(repaired, f.Kind+" "+f.Key())
	}
	for _, f := range out.Drift {
		drift = append(drift, f.Kind+" "+f.Key())
	}
	return repaired, drift
}

// AssignedAgents names the slots an assign took: two for a best-of:2 issue.
func AssignedAgents(res round.Assigned) []string {
	if len(res.BestOf) == 2 {
		return []string{res.BestOf[0].Agent, res.BestOf[1].Agent}
	}
	return []string{res.Agent}
}
