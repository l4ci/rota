package cli

import (
	"flag"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/gate"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/roundtick"
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

// windDownSummary folds the closed round's ledger into the same table
// `rota round summary` prints. ok is false when there is no ledger or nothing
// for that round, so wind-down adds nothing.
func windDownSummary(root string, rnd int) (obj *jsonx.Object, text string, ok bool) {
	entries, err := ledger.Load(root)
	if err != nil || len(entries) == 0 {
		return nil, "", false
	}
	audit, _ := gate.ReadAudit(root)
	s := ledger.Fold(entries, audit, rnd)
	if len(s.Issues) == 0 {
		return nil, "", false
	}
	return s.Object(), s.Text(), true
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
		if raw, err := openBacklog(c, root, false, ""); err == nil {
			board, _ = round.BoardOf(raw)
		}
		env := c.deps().RoundEnv(ctx, root)
		env.Worker = workerEnvCtx(c, ctx)
		// An autopilot watch must not assign or merge while the base is being
		// re-verified; a kept lease lets it resume.
		var cd string
		var rnd int
		if d, err := rotastate.CommonDir(root); err == nil {
			if l, _, err := c.deps().LeaseEnv().Read(d); err == nil {
				cd, rnd = d, l.Round
				_ = roundtick.SetStopped(cd, rnd, true)
			}
		}
		res, err := env.WindDown(ctx, root, board, round.WindDownOpts{
			NoVerify: *noVerify, HolderPID: *pid, Settings: set,
		})
		if cd != "" {
			if err != nil || res.Retained {
				_ = roundtick.SetStopped(cd, rnd, false)
			} else {
				roundtick.ClearState(cd)
			}
		}
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
		if sum, text, ok := windDownSummary(root, res.Round); ok {
			d.Set("summary", sum)
			lines = append(lines, "", text)
		}
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
