package cli

import (
	"context"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/round"
)

const observationNotice = "Observation only: not approval or landing proof; execution re-reads state and checks every gate."

func tickNudge(c *Ctx, root string, d *jsonx.Object, observed bool) []string {
	if observed {
		return nil
	}
	return addTrainNudge(c, root, d, nil)
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
