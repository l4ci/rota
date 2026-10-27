package cli

import (
	"flag"

	"github.com/l4ci/rota/internal/gate"
	"github.com/l4ci/rota/internal/ledger"
)

// roundSummaryVerb folds a round's ledger and the gate audit log into the table the
// orchestrator reads at wind-down: a row per issue, totals per slot and per
// account. The round is the lease's, else the highest in the ledger; --round
// picks another. No ledger yet is not an error.
func roundSummaryVerb(fs *flag.FlagSet) RunFunc {
	want := fs.Int("round", -1, "the round number; default is the lease's round, else the highest in the ledger")
	return func(c *Ctx, args []string) (Result, error) {
		if err := noArgs(args); err != nil {
			return Result{}, err
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		entries, sourceErr := ledger.LoadForSummary(root)
		n := *want
		if n < 0 {
			n = currentRound(c, root, entries)
		}
		known := false
		for _, e := range entries {
			known = known || e.Round == n
		}
		if !known && *want >= 0 && len(entries) > 0 && sourceErr == nil {
			return Result{}, Resolution("round %d has no ledger entries", n)
		}
		audit, err := gate.ReadAudit(root)
		if err != nil {
			return Result{}, err
		}
		s := ledger.Fold(entries, audit, n)
		if sourceErr != nil {
			s.UnavailableMetrics(sourceErr.Error())
		}
		text := s.Text()
		if len(entries) == 0 && (sourceErr == nil || !ledger.Exists(root)) {
			text = "no ledger yet: it fills as rounds assign, report and gate\n" + text
		}
		return Result{Data: s.Object(), Text: text}, nil
	}
}
