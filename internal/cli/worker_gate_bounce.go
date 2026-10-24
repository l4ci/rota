package cli

import (
	"github.com/l4ci/rota/internal/round"
)

// parkNeedsHuman is the postgate.Park of a gate or train run: it hands the item
// to a human as `round transfer --to human` does (the PR stays open).
func parkNeedsHuman(c *Ctx, root string) func(issue, note string) error {
	return func(issue, note string) error {
		env, be, err := moveEnv(c, root)
		if err != nil {
			return err
		}
		_, err = env.Transfer(c.Context(), root, be, round.TransferOpts{Issue: issue, To: round.HumanTarget, Note: note})
		return err
	}
}
