package cli

import (
	"flag"

	"github.com/l4ci/rota/internal/gate"
	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundlease"
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
		entries, err := ledger.Load(root)
		if err != nil {
			return Result{}, err
		}
		if len(entries) == 0 {
			return Result{Data: ledger.Fold(nil, nil, 0).Object(), Text: "no ledger yet: it fills as rounds assign, report and gate"}, nil
		}
		n := *want
		if n < 0 {
			for _, e := range entries {
				n = max(n, e.Round)
			}
			env := round.Env{Git: c.deps().Git, Lease: c.deps().LeaseEnv()}
			if l, st, err := env.ReadLease(c.Context(), root); err == nil && (st == roundlease.Live || st == roundlease.Foreign) {
				n = l.Round
			}
		}
		known := false
		for _, e := range entries {
			known = known || e.Round == n
		}
		if !known && *want >= 0 {
			return Result{}, Resolution("round %d has no ledger entries", n)
		}
		audit, err := gate.ReadAudit(root)
		if err != nil {
			return Result{}, err
		}
		s := ledger.Fold(entries, audit, n)
		return Result{Data: s.Object(), Text: s.Text()}, nil
	}
}
