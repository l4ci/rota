package cli

import (
	"flag"
	"fmt"
	"os"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/round"
)

func roundReclaim(fs *flag.FlagSet) RunFunc {
	force := fs.Bool("force", false, "reclaim a healthy slot too (kills its pane)")
	note := fs.String("note-file", "", "what is done and what is next (- for stdin)")
	pid := fs.Int("holder-pid", 0, "orchestrator pid, when its ancestry cannot be read")
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) != 1 {
			return Result{}, Usage("round reclaim takes one slot name")
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
		res, err := env.Reclaim(c.Context(), root, be, round.ReclaimOpts{
			Slot: args[0], Force: *force, Note: text, HolderPID: *pid, Getenv: os.Getenv,
		})
		if err != nil {
			return moveFailure(err, res.Changed)
		}
		d := jsonx.NewObject()
		d.Set("slot", res.Slot)
		setIf(d, "issue", res.Issue)
		setIf(d, "branch", res.Branch)
		setIf(d, "head", sha7(res.Head))
		d.Set("health", res.Health)
		d.Set("salvaged", res.Salvaged)
		d.Set("released", res.Released)
		d.Set("parked", res.Parked)
		d.Set("changed", res.Changed)
		text = fmt.Sprintf("%s is idle: nothing to reclaim", res.Slot)
		if res.Changed {
			text = fmt.Sprintf("reclaimed %s from %s (%s); branch %s kept", res.Issue, res.Slot, res.Health, res.Branch)
		}
		return Result{Data: d, Text: text}, nil
	}
}
