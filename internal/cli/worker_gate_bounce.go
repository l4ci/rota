package cli

import (
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/worker"
)

// gateIssue is the issue a `worker gate` target stands for: the queued
// record's, else the one the slot holds. "" when it cannot be told.
func gateIssue(root, target string) string {
	reg, err := worker.LoadRegistry(root)
	if err != nil {
		return "" // the gate itself refused a corrupt registry before this is asked
	}
	t, err := reg.GateTargetAny(target)
	if err != nil {
		return ""
	}
	if t.Queued {
		return t.Issue
	}
	return worker.HeldID(t.Task, t.Branch, t.Name)
}

// parkNeedsHuman is the postgate.Park of a gate or train run: it hands the item
// to a human as `round transfer --to human` does (the PR stays open).
func parkNeedsHuman(c *Ctx, root string) func(issue, note string, set roundcfg.Settings) error {
	return func(issue, note string, set roundcfg.Settings) error {
		env, be, err := moveEnv(c, root)
		if err != nil {
			return err
		}
		_, err = env.Transfer(c.Context(), root, be, round.TransferOpts{Issue: issue, To: round.HumanTarget, Note: note, Settings: set})
		return err
	}
}
