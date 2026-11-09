package cli

import (
	"errors"

	"github.com/l4ci/rota/internal/jsonx"
)

const observationNotice = "Observation only: not approval or landing proof; execution re-reads state and checks every gate."

func tickNudge(c *Ctx, root string, d *jsonx.Object, observed bool) []string {
	if observed {
		return nil
	}
	return addTrainNudge(c, root, d, nil)
}

// errReadOnlyWorld is what an observing tickWorld answers to anything that
// would change the world; roundtick.Run proposes those steps instead of
// calling them, so reaching one is a bug the refusal makes visible.
var errReadOnlyWorld = errors.New("read-only world: observation never changes state")
