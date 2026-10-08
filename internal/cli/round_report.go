package cli

import (
	"flag"
	"fmt"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/round"
)

// The C8 verb `rota round report`: a solo round's stand-in for the pane poll
// that writes a slot's state; the rules are round.ReportSlot.
func roundReport(fs *flag.FlagSet) RunFunc {
	state := fs.String("state", "", "done, blocked, idle, dead or limited")
	evidence := fs.String("evidence", "", "what the worker's result said, echoed and not stored")
	pr := fs.String("pr", "", "the worker's PR, as a URL or a number")
	issues := fs.String("issues", "", "a review item's filed issues, comma-separated (#139,#140), in place of a PR")
	return func(c *Ctx, args []string) (Result, error) {
		slot, err := oneArg(args, "slot")
		if err != nil {
			return Result{}, err
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		env := round.Env{Accounts: c.deps().WorkerAccounts()}
		res, err := env.ReportSlot(c.Context(), root, round.ReportOpts{Slot: slot, State: *state, Evidence: *evidence, PR: *pr, Issues: *issues})
		if err != nil {
			return Result{}, err
		}
		d := jsonx.NewObject()
		d.Set("slot", res.Slot)
		d.Set("state", res.State)
		d.Set("previous", res.Previous)
		setIf(d, "pr", res.PR)
		if len(res.Issues) > 0 {
			d.Set("issues", res.Issues)
		}
		setIf(d, "evidence", res.Evidence)
		d.Set("changed", res.Changed)
		return Result{Data: d, Text: fmt.Sprintf("%s\t%s", res.Slot, res.State)}, nil
	}
}
