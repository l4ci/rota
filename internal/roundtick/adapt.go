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

// DriftKind is the kind of a reconcile finding (round.Finding.Kind). It crosses
// the seam typed, so the tick can branch on it without parsing a line.
type DriftKind string

// Finding is one reconcile finding as the tick carries it: its kind and its key.
type Finding struct {
	Kind DriftKind
	Key  string
}

// String is the finding's display and audit form, "kind key".
func (f Finding) String() string { return string(f.Kind) + " " + f.Key }

// FindingOf maps a reconcile finding to the tick's typed one.
func FindingOf(f round.Finding) Finding { return Finding{Kind: DriftKind(f.Kind), Key: f.Key()} }

// ReconcileFindings maps a reconcile outcome to what it repaired and the drift
// it left.
func ReconcileFindings(out round.Outcome) (repaired, drift []Finding) {
	for _, f := range out.Repaired {
		repaired = append(repaired, FindingOf(f))
	}
	for _, f := range out.Drift {
		drift = append(drift, FindingOf(f))
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
