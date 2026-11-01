package cli

import (
	"context"

	"github.com/l4ci/rota/internal/postgate"
	"github.com/l4ci/rota/internal/verdict"
)

// gateReportTransport delivers a report prepared by the domain lifecycle.
func gateReportTransport(c *Ctx) func(context.Context, string, postgate.Report) error {
	return func(_ context.Context, root string, r postgate.Report) error {
		t := verdictBranch{root: root, branch: r.Gate.Branch, ref: r.Ref, dir: root}
		var items []verdict.ItemVerdict
		if r.Gate.Issue != "" {
			items = append(items, verdict.ItemVerdict{ID: r.Gate.Issue})
		}
		return resolveReportTargets(c, t, r.Gate.PR, items).post(c, r.Body)
	}
}

func landingEnv(c *Ctx, ctx context.Context, root string) postgate.Env {
	return postgate.Env{Worker: workerEnvCtx(c, ctx), Report: gateReportTransport(c), Park: parkNeedsHuman(c, root)}
}

func reportWarnings(c *Ctx, warnings []string) {
	for _, w := range warnings {
		c.Warn("%s", w)
	}
}
