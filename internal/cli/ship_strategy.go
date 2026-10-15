package cli

import (
	"flag"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/tracker"
)

// shipStrategy is `rota ship strategy`: how the branch lands. The skills read
// it instead of work.mergeStrategy alone, so the routing honors the forge:
//
//   - issues on a tracker (issues.source github or gitlab): pr, always;
//   - a local forge (forge.provider local, or auto with no origin remote): direct,
//     whatever work.mergeStrategy says, since there is no PR to open;
//   - otherwise work.mergeStrategy: direct, pr, or unset.
//
// Issues on a tracker with a local forge cannot be served: exit 3, set
// forge.provider or the origin.
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
		cfg := config.Load(rotatree.Config(root))
		backend, _, err := backendMode(root)
		if err != nil {
			return Result{}, err
		}
		fp, _ := config.ForgeProvider(cfg)
		forge, ferr := tracker.ResolveProvider(c.Context(), "", fp, dir, c.deps().trackerExec())
		if ferr != nil {
			forge = tracker.ProviderUnknown // an origin that names no forge: the configured strategy stands
		}
		configured := config.StringIfSet(cfg, "work.mergeStrategy")
		strategy, reason := configured, "work.mergeStrategy"
		if strategy == "" {
			strategy = "unset"
		}
		switch {
		case backend == "issues" && forge == tracker.ProviderLocal:
			return Result{Data: jsonObj("strategy", "", "forge", forge, "changed", false)},
				Resolution("issues are on a tracker, which needs a PR, but the forge is local (no origin remote, or forge.provider local): set forge.provider or an origin remote")
		case backend == "issues":
			strategy, reason = "pr", "issues.source is a tracker: the PR closes the issues"
		case forge == tracker.ProviderLocal:
			strategy, reason = "direct", "the forge is local: there are no pull requests"
		}
		return Result{Data: jsonObj("strategy", strategy, "forge", forge, "reason", reason),
			Text: strategy + " (" + reason + ")"}, nil
	}
}
