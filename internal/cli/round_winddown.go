package cli

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
)

// The C3 verb `rota round wind-down`; the steps are round.Env.WindDown.

func slotOutcomeList(slots []round.SlotOutcome) []any {
	out := make([]any, 0, len(slots))
	for _, s := range slots {
		d := jsonx.NewObject()
		d.Set("name", s.Name)
		d.Set("outcome", s.Outcome)
		setIf(d, "issue", s.Issue)
		setIf(d, "pr", s.PR)
		if s.Merged != nil {
			d.Set("merged", *s.Merged)
		}
		if len(s.Dirty) > 0 {
			d.Set("dirty", strs(s.Dirty))
		}
		if len(s.Unmerged) > 0 {
			d.Set("unmerged", strs(s.Unmerged))
		}
		out = append(out, d)
	}
	return out
}

func roundWindDown(fs *flag.FlagSet) RunFunc {
	noVerify := fs.Bool("no-verify", false, "skip the re-verify of the base")
	pid := fs.Int("holder-pid", 0, "orchestrator pid, when its ancestry cannot be read")
	return func(c *Ctx, args []string) (Result, error) {
		if err := noArgs(args); err != nil {
			return Result{}, err
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		set, err := roundcfg.Load(root)
		if err != nil {
			return Result{}, &Error{Exit: ExitInternal, Message: err.Error()}
		}
		ctx, stop := workerContext()
		defer stop()
		var board round.Board
		if raw, err := a4Open(c, root, false, ""); err == nil {
			board, _ = raw.(round.Board)
		}
		env := roundEnv(ctx, root)
		env.Worker = workerEnvCtx(ctx)
		res, err := env.WindDown(ctx, root, board, round.WindDownOpts{
			NoVerify: *noVerify, HolderPID: *pid, Settings: set, Getenv: os.Getenv,
		})
		if err != nil {
			return Result{}, err
		}
		for _, w := range res.Warnings {
			c.Warn("%s", w)
		}
		d := jsonx.NewObject()
		if res.Retained && res.Verdict == round.VerdictHoldsWork {
			d.Set("blockedBy", "slot holds work")
		}
		d.Set("round", res.Round)
		d.Set("base", res.Base)
		d.Set("verdict", res.Verdict)
		d.Set("slots", slotOutcomeList(res.Slots))
		d.Set("verified", strs(res.Verified))
		d.Set("verifySkipped", res.VerifySkipped)
		d.Set("drift", res.Drift)
		if res.Lease != nil {
			d.Set("lease", leaseData(*res.Lease, round.LeaseState("live")))
		}
		d.Set("changed", res.Changed)
		var lines []string
		for _, s := range res.Slots {
			lines = append(lines, fmt.Sprintf("%s\t%s\t%s", s.Name, s.Outcome, dash(s.Issue)))
		}
		lines = append(lines, "verdict\t"+res.Verdict)
		out := Result{Data: d, Text: strings.Join(lines, "\n")}
		switch res.Verdict {
		case round.VerdictVerifyFailed:
			if res.VerifyLog != "" {
				c.Warn("verify output (tail):\n%s", res.VerifyLog)
			}
			return out, Failed("the base does not pass verification; the lease is kept")
		case round.VerdictHoldsWork:
			return out, Refused("a slot still holds work; the others are parked and the lease is kept")
		}
		return out, nil
	}
}
