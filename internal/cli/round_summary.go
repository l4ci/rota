package cli

import (
	"flag"
	"strings"

	"github.com/l4ci/rota/internal/gate"
	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/worker"
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
		source := ledger.LoadForSummary(root)
		n := *want
		if n < 0 {
			n = currentRound(c, root, source.Entries, source.HighestRound())
		}
		if source.Loaded() && !source.HasRound(n) && *want >= 0 {
			return Result{}, Resolution("round %d has no ledger entries", n)
		}
		audit, err := gate.ReadAudit(root)
		if err != nil {
			return Result{}, err
		}
		s := source.Fold(audit, n)
		s.ReadFlakes(root)
		text := s.Text()
		if !ledger.Exists(root) {
			text = "no ledger yet: it fills as rounds assign, report and gate\n" + text
		}
		ctx, stop := workerContext()
		defer stop()
		refs := make([]worker.QuotaAccount, 0, len(s.Issues))
		for _, row := range s.Issues {
			refs = append(refs, worker.QuotaAccount{Kind: row.Harness, Name: row.Account})
		}
		snapshots := c.deps().AccountQuotas().ForAccounts(ctx, root, refs)
		data := s.Object()
		quotas := make([]any, 0, len(snapshots))
		var quotaLines []string
		for _, snapshot := range snapshots {
			o, err := quotaObject(snapshot)
			if err != nil {
				return Result{}, err
			}
			quotas = append(quotas, o)
			quotaLines = append(quotaLines, snapshot.Kind+"/"+snapshot.Name+" "+accountQuotaText(o))
		}
		data.Set("accountQuotas", quotas)
		if len(quotaLines) > 0 {
			text += "\n\nCurrent account quota (observed now; not round usage)\n" + strings.Join(quotaLines, "\n")
		}
		return Result{Data: data, Text: text}, nil
	}
}
