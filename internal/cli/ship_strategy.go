package cli

import (
	"errors"
	"flag"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/ship"
	"github.com/l4ci/rota/internal/tracker"
)

// shipStrategy is `rota ship strategy`: how the branch lands. The skills read
// it instead of work.mergeStrategy alone. The routing rule lives in
// ship.Strategy; this verb resolves its inputs and renders the answer.
// Issues on a tracker with a local forge cannot be served: exit 3.
func shipStrategy(*flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 0, 0, "ship strategy takes no arguments"); err != nil {
			return Result{}, err
		}
		root, err := backlogScope(c)
		if err != nil {
			return Result{}, err
		}
		dir, err := c.RepoPath()
		if err != nil {
			return Result{}, err
		}
		caps, cfg, err := backendMode(c.deps(), root)
		if err != nil {
			return Result{}, err
		}
		fp, _ := config.ForgeProvider(cfg)
		r, ferr := tracker.ResolveProvider(c.Context(), tracker.ForgeActor, "", fp, dir, c.deps().trackerExec())
		forge := r.Provider
		if ferr != nil {
			forge = tracker.ProviderUnknown // an origin that names no forge: the configured strategy stands
		}
		landing, err := ship.Strategy(caps, forge, config.StringIfSet(cfg, "work.mergeStrategy"))
		if errors.Is(err, ship.ErrTrackerNeedsLocalForge) {
			return Result{Data: jsonObj("strategy", "", "forge", forge, "changed", false)}, Resolution(err.Error())
		}
		return Result{Data: jsonObj("strategy", landing.Strategy, "forge", forge, "reason", landing.Reason),
			Text: landing.Strategy + " (" + landing.Reason + ")"}, nil
	}
}
