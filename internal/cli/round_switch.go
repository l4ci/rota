package cli

import (
	"flag"
	"fmt"

	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/round"
)

func roundSwitch(fs *flag.FlagSet) RunFunc {
	account := fs.String("account", "", "the account the slot moves to (default: the usable one with the most headroom, never the current one)")
	kind := fs.String("kind", "", "move the slot to this harness: "+harness.KindList()+" (default: keep the slot's harness)")
	model := fs.String("model", "", "the model of the new session (default: the slot's, or the tier map's on a harness change)")
	note := fs.String("note-file", "", "a note appended to the new session's brief (- for stdin)")
	return holderPIDFlag(fs, "orchestrator pid, when its ancestry cannot be read", func(c *Ctx, args []string) (Result, error) {
		if len(args) != 1 {
			return Result{}, Usage("round switch takes one slot name")
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		text, err := roundNote(c, "note-file", *note)
		if err != nil {
			return Result{}, err
		}
		env, be, err := moveEnv(c, root)
		if err != nil {
			return Result{}, err
		}
		res, err := env.Switch(c.Context(), root, be, round.SwitchOpts{Slot: args[0], Account: *account, Kind: *kind, Model: *model, Note: text})
		if err != nil {
			r, ferr := moveFailure(err, res.Changed)
			if res.Changed {
				r.Data = switchData(res)
				if !res.Dispatched && res.ToAccount != "" {
					c.Warn("the checkpoint is pushed and the slot records %s, but no new session started: retry with `rota round switch %s --account %s`", accountText(res.ToAccount), res.Slot, res.ToAccount)
				}
			}
			return r, ferr
		}
		for _, w := range res.Warnings {
			c.Warn("%s", w)
		}
		cp := "no checkpoint"
		if res.Checkpoint != "" {
			cp = "checkpoint " + res.Checkpoint
		}
		cp += ", pushed to the branch (a recorded PR now shows it)"
		return Result{Data: switchData(res), Text: fmt.Sprintf("switched %s (%s) from %s/%s to %s/%s, %s",
			res.Slot, res.Issue, res.FromKind, accountText(res.FromAccount), res.ToKind, accountText(res.ToAccount), cp)}, nil
	})
}

func accountText(name string) string {
	if name == "" {
		return "ambient login"
	}
	return name
}

func switchData(res round.Switched) *jsonx.Object {
	d := jsonx.NewObject()
	d.Set("slot", res.Slot)
	d.Set("issue", res.Issue)
	d.Set("bundle", jsonx.Strings(res.Bundle))
	d.Set("branch", res.Branch)
	d.Set("attempt", res.Attempt)
	d.Set("fromKind", res.FromKind)
	d.Set("toKind", res.ToKind)
	d.Set("fromAccount", res.FromAccount)
	d.Set("toAccount", res.ToAccount)
	d.Set("checkpoint", res.Checkpoint)
	d.Set("dispatched", res.Dispatched)
	d.Set("changed", res.Changed)
	return d
}
