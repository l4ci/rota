package round

import (
	"context"
	"slices"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/worker"
)

// CrossPools reads the accounts a cross-harness review may run on, with their
// meters (#3315): review.accounts as the first choice, and round.accounts of
// every harness the round may use as the fallback. The fallback leaves out a
// harness whose only logins are review accounts. Headroom is on the scale
// worker placement uses (100 minus the most-used window), so a Claude 5h/7d
// meter and a Codex weekly one compare directly.
func (e Env) CrossPools(ctx context.Context, root string) (worker.CrossPools, error) {
	cfg := e.Worker.Config(root)
	home, _ := e.Worker.Home()
	set := e.Round
	var reviewKinds []string
	for _, a := range config.ReviewAccounts(cfg) {
		reviewKinds = append(reviewKinds, a.Kind)
	}
	mainKinds := allowedKinds(cfg, set)
	ms := e.readMeters(ctx, root, append(slices.Clone(reviewKinds), mainKinds...), config.ReviewAccounts(cfg)...)
	var out worker.CrossPools
	for _, a := range config.ReviewAccounts(cfg) {
		hz := harness.ResolveAccounts(a.Kind)
		list := harness.AccountList{Entries: []harness.AccountEntry{{Name: a.Name, Dir: a.Dir}}}.In(home).Entries
		out.Review = append(out.Review, e.poolEntries(ctx, ms, hz.Kind(), list, set, len(out.Review), nil)...)
	}
	for _, kind := range mainKinds {
		hz := harness.ResolveAccounts(kind)
		list := harness.AccountsFor(cfg, hz.Kind(), home).Entries
		out.Main = append(out.Main, e.poolEntries(ctx, ms, hz.Kind(), list, set, len(out.Main), nil)...)
	}
	return out, nil
}
