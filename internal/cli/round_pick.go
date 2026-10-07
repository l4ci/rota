package cli

import (
	"flag"
	"fmt"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/round"
)

func roundPick(fs *flag.FlagSet) RunFunc {
	pr := fs.String("pr", "", "the attempt's PR that may merge (number, #number or URL)")
	reason := fs.String("reason-file", "", "why this attempt won; posted on the closed PR (- for stdin)")
	pid := fs.Int("holder-pid", 0, "orchestrator pid, when its ancestry cannot be read")
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) != 1 {
			return Result{}, Usage("round pick takes one item ID")
		}
		if *pr == "" {
			return Result{}, Usage("--pr is required: the attempt's PR that may merge")
		}
		if *reason == "" {
			return Result{}, Usage("--reason-file is required: say why this attempt won")
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		text, err := roundNote(c, "reason-file", *reason)
		if err != nil {
			return Result{}, err
		}
		env, be, err := moveEnv(c, root)
		if err != nil {
			return Result{}, err
		}
		id, _, err := resolveItem(be, args[0])
		if err != nil {
			return backlogFail(err)
		}
		res, err := env.Pick(c.Context(), root, be, round.PickOpts{ID: id, PR: *pr, Reason: text, HolderPID: *pid})
		if err != nil {
			return moveFailure(err, res.Changed)
		}
		for _, w := range res.Warnings {
			c.Warn("%s", w)
		}
		out := fmt.Sprintf("#%s was already picked: %s (%s)", res.ID, res.Winner.PR, res.Winner.Slot)
		if res.Changed {
			out = fmt.Sprintf("picked %s (%s) for #%s", res.Winner.PR, res.Winner.Slot, res.ID)
			switch {
			case res.Closed:
				out += fmt.Sprintf("; closed %s (%s)", res.Loser.PR, res.Loser.Slot)
			case res.Loser.Slot != "":
				out += fmt.Sprintf("; %s (%s) had no open PR to close", res.Loser.Slot, res.Loser.State)
			}
		}
		return Result{Data: pickData(res), Text: out}, nil
	}
}

func pickData(res round.Picked) *jsonx.Object {
	attempt := func(a round.PickAttempt, withState bool) *jsonx.Object {
		o := jsonx.NewObject()
		o.Set("slot", a.Slot)
		o.Set("branch", a.Branch)
		o.Set("pr", a.PR)
		if withState {
			o.Set("state", a.State)
		}
		return o
	}
	d := jsonx.NewObject()
	d.Set("id", res.ID)
	d.Set("winner", attempt(res.Winner, false))
	d.Set("loser", attempt(res.Loser, true))
	d.Set("closed", res.Closed)
	d.Set("changed", res.Changed)
	d.Set("warnings", append([]string{}, res.Warnings...))
	return d
}
