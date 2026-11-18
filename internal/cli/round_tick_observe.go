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

// inspectingWorld supplies no-write variants of reads that otherwise cache or
// repair state. roundtick.NewEnv owns refusing execution adapters.
type inspectingWorld struct{ *tickWorld }

func (o inspectingWorld) Observing() bool { return true }
func (o inspectingWorld) Reconcile(ctx context.Context) (round.Outcome, error) {
	return o.renv.InspectReconcile(ctx, o.root)
}
func (o inspectingWorld) ReviewReadiness(slot, base string) (string, bool) {
	return o.reviewReadiness(slot, base, true)
}
func (o inspectingWorld) ReviewLoop(slot string) roundtick.ReviewOutcome {
	src := reviewSources(o.c, o.root, o.set, slot)
	src.Observe = true
	return roundtick.ReviewStep(o.c.Context(), src)
}
func (o inspectingWorld) Review() roundtick.Reviewer {
	return inspectingReviewer{o.tickWorld.Review().(reviewAdapter)}
}

// inspectingReviewer reads architecture facts without updating review counters.
type inspectingReviewer struct{ reviewAdapter }

func (r inspectingReviewer) Facts(ctx context.Context) (round.Architecture, bool, error) {
	cands, err := r.renv.RoundCandidates(ctx, r.root, r.board, r.set, "")
	if err != nil {
		return round.Architecture{}, false, err
	}
	return r.renv.InspectArchitectureFacts(ctx, r.root, r.board, r.set, cands)
}
