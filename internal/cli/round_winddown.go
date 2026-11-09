package cli

import (
	"flag"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/bugreport"
	"github.com/l4ci/rota/internal/gate"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/receipt"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundlease"
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
			d.Set("dirty", jsonx.Strings(s.Dirty))
		}
		if len(s.Unmerged) > 0 {
			d.Set("unmerged", jsonx.Strings(s.Unmerged))
		}
		out = append(out, d)
	}
	return out
}

// windDownSummary folds the closed round's ledger into the same table
// `rota round summary` prints. ok is false when there is no ledger or nothing
// for that round, so wind-down adds nothing.
func windDownSummary(root string, rnd int) (obj *jsonx.Object, text string, ok bool) {
	source := ledger.LoadForSummary(root)
	if !source.HasRound(rnd) {
		return nil, "", false
	}
	audit, _ := gate.ReadAudit(root)
	s := source.Fold(audit, rnd)
	s.ReadFlakes(root)
	return s.Object(), s.Text(), true
}

func roundWindDown(fs *flag.FlagSet) RunFunc {
	noVerify := fs.Bool("no-verify", false, "skip the re-verify of the base")
	prune := fs.Bool("prune", false, "also delete the worktree and branch of a released adopted slot")
	pid := fs.Int("holder-pid", 0, "orchestrator pid, when its ancestry cannot be read")
	return func(c *Ctx, args []string) (Result, error) {
		if err := noArgs(args); err != nil {
			return Result{}, err
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		set, err := c.deps().roundSettings(root, roundConfigUsage)
		if err != nil {
			return Result{}, err
		}
		ctx, stop := workerContext()
		defer stop()
		var board round.Board
		if raw, err := openBacklog(c, root, false, ""); err == nil {
			board, _ = round.BoardOf(raw)
		}
		env := c.deps().RoundEnv(ctx, root)
		env.Worker = round.FromWorkerEnv(workerEnvCtx(c, ctx))
		// An autopilot watch must not assign or merge while the base is being
		// re-verified; a kept lease lets it resume, a failed verify keeps it stopped.
		var cd string
		var rnd int
		var priorStopped bool
		var priorReason string
		if d, ok := c.CommonDirOK(root); ok {
			if l, _, err := c.deps().LeaseEnv().Read(d); err == nil {
				cd, rnd = d, l.Round
				ps := roundtick.LoadState(cd, rnd)
				priorStopped, priorReason = ps.Stopped, ps.StoppedReason
				_ = roundtick.SetStopped(cd, rnd, true)
			}
		}
		res, err := env.WindDown(ctx, root, board, round.WindDownOpts{
			NoVerify: *noVerify, Prune: *prune, HolderPID: *pid, Settings: set,
		})
		// The autopilot stays stopped from a red verify until a wind-down
		// verifies green. A failed verify stops it whatever else went wrong; a
		// call that did not verify (error before the verify, or --no-verify)
		// restores what it found; a green verify with work still held resumes
		// it; a finished round clears the state.
		verifyRed := res.Verdict == round.VerdictVerifyFailed
		if cd != "" {
			switch {
			case verifyRed:
				_ = roundtick.SetStop(cd, rnd, true, roundtick.StopVerifyFailed)
			case err != nil || (res.VerifySkipped && !res.Over()):
				_ = roundtick.SetStop(cd, rnd, priorStopped, priorReason)
			case !res.Over():
				_ = roundtick.SetStopped(cd, rnd, false)
			default:
				roundtick.ClearState(cd)
			}
		}
		stoppedLine := "autopilot\tstays stopped; fix the base, then run `rota round wind-down` again (plain `rota round watch` still works)"
		// Warnings first: a refusal must not hide the buffered-hits fold command.
		for _, w := range res.Warnings {
			c.Warn("%s", w)
		}
		if err != nil {
			if verifyRed && cd != "" {
				c.Warn("%s", strings.Replace(stoppedLine, "\t", " ", 1))
			}
			return Result{}, err
		}
		if res.Over() { // the round is over: no retry can reach its receipts
			if perr := receipt.Prune(root); perr != nil {
				c.Warn("receipts not pruned: %v", perr)
			}
		}
		d := jsonx.NewObject()
		if res.Retained && res.Verdict == round.VerdictHoldsWork {
			d.Set("blockedBy", blockedby.SlotHoldsWork)
		}
		d.Set("round", res.Round)
		d.Set("base", res.Base)
		d.Set("verdict", res.Verdict)
		d.Set("slots", slotOutcomeList(res.Slots))
		d.Set("verified", jsonx.Strings(res.Verified))
		d.Set("verifySkipped", res.VerifySkipped)
		d.Set("drift", res.Drift)
		if res.Lease != nil {
			d.Set("lease", leaseData(*res.Lease, roundlease.Live))
		}
		d.Set("changed", res.Changed)
		var lines []string
		for _, s := range res.Slots {
			lines = append(lines, fmt.Sprintf("%s\t%s\t%s", s.Name, s.Outcome, dash(s.Issue)))
		}
		lines = append(lines, "verdict\t"+res.Verdict)
		if verifyRed && cd != "" {
			d.Set("autopilotStopped", true)
			lines = append(lines, stoppedLine)
		}
		if sum, text, ok := windDownSummary(root, res.Round); ok {
			d.Set("summary", sum)
			lines = append(lines, "", text)
		}
		if open, _ := bugreport.Open(c.Context(), c.deps().Git, root); len(open) > 0 {
			d.Set("bugreports", len(open))
			lines = append(lines, "", bugreportOffer(open))
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
