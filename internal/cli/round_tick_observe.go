package cli

import (
	"context"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundtick"
)

const observationNotice = "Observation only: not approval or landing proof; execution re-reads state and checks every gate."

func tickNudge(c *Ctx, root string, d *jsonx.Object, observed bool) []string {
	if observed {
		return nil
	}
	return addTrainNudge(c, root, d, nil)
}

func (w *tickWorld) InspectReconcile(ctx context.Context) (roundtick.Inspection, error) {
	out, err := w.renv.InspectReconcile(ctx, w.root)
	var inspection roundtick.Inspection
	for _, f := range out.Proposed {
		inspection.Proposed = append(inspection.Proposed, f.Kind+" "+f.Key())
	}
	// InspectReconcile retains all drift, including repairable findings. A
	// proposed repair belongs in proposals rather than also an unrepairable hold.
	proposed := map[string]bool{}
	for _, f := range inspection.Proposed {
		proposed[f] = true
	}
	for _, f := range out.Drift {
		line := f.Kind + " " + f.Key()
		if !proposed[line] {
			inspection.Drift = append(inspection.Drift, line)
		}
	}
	if out.Report != nil {
		for _, source := range out.Report.Unavailable {
			inspection.Unknown = append(inspection.Unknown, roundtick.Item{Kind: "source", Target: source, Why: "reconciliation source unavailable; dependent repairs are not verified"})
		}
		for _, warning := range out.Report.Warnings {
			inspection.Unknown = append(inspection.Unknown, roundtick.Item{Kind: "reconcile", Target: "round", Why: warning})
		}
	}
	return inspection, err
}

func (w *tickWorld) InspectReviewFacts(ctx context.Context) (round.Architecture, bool, error) {
	candidates, err := w.renv.RoundCandidates(ctx, w.root, w.board, w.set, "")
	if err != nil {
		return round.Architecture{}, false, err
	}
	return w.renv.InspectArchitectureFacts(ctx, w.root, w.board, w.set, candidates)
}

func (w *tickWorld) InspectReviewReadiness(slot, base string) (string, bool) {
	return reviewReadinessMode(w.c, w.root, slot, base, true)
}

func (w *tickWorld) InspectReviewLoop(slot string) roundtick.ReviewOutcome {
	return reviewStepMode(w.c, w.root, w.set, slot, true)
}

// Keep the compile-time boundary explicit: observation does not fall back to
// World.Reconcile, ReviewLoop or Reviewer.Facts when a probe is unavailable.
var _ roundtick.ObservationWorld = (*tickWorld)(nil)
