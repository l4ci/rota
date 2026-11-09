package cli

import (
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/stalebin"
)

func staleData(f stalebin.Finding) *jsonx.Object {
	return jsonx.Obj("commit", f.Commit, "head", f.Head, "behind", f.Behind, "rebuild", f.Rebuild)
}

// staleBinaryOncePerRound is the round heartbeat's copy of doctor's binary
// warning. The once-per-round rule and its marker belong to round
// (round.ClaimStaleWarning); a marker error comes back as err beside the finding.
func staleBinaryOncePerRound(c *Ctx, cd string, n int) (f stalebin.Finding, ok bool, err error) {
	dir, err := c.deps().Getwd()
	if err != nil {
		return stalebin.Finding{}, false, err
	}
	f, ok = staleBinary(c.Context(), c.deps().Git, dir)
	if !ok {
		return stalebin.Finding{}, false, nil
	}
	first, err := round.ClaimStaleWarning(cd, n)
	return f, first, err
}
