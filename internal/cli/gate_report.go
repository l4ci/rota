package cli

import (
	"context"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/labels"
	"github.com/l4ci/rota/internal/postgate"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/verdict"
	"github.com/l4ci/rota/internal/worker"
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
	env := workerEnvCtx(c, ctx).Landing(releaseLandedSlice(c))
	env.Report = gateReportTransport(c)
	return env
}

// releaseLandedSlice gives up slot's claim on an issue whose PR landed while
// the issue stayed open for later slices (#3629): the issue carries
// partial-slice and is not closed. Any other issue, and a claim another slot
// holds, is left alone. The backlog opens on first use.
func releaseLandedSlice(c *Ctx) func(root, issue, slot string) error {
	return func(root, issue, slot string) error {
		raw, err := openBacklog(c, root, false, "")
		if err != nil {
			return err
		}
		board, ok := round.BoardOf(raw)
		if !ok {
			return nil
		}
		it, err := raw.Get(issue)
		if err != nil || it.Closed || !slices.Contains(it.Labels, labels.PartialSlice) {
			return nil
		}
		tr, ok := backlog.ItemTrackerFor(raw)
		if !ok {
			return nil
		}
		st, err := tr.Status(issue)
		if err != nil {
			return err
		}
		for _, claim := range st.Claims {
			if strings.HasPrefix(claim, slot+"@") {
				if _, err := board.Release(issue, claim); err != nil {
					return err
				}
				// The slot still holds the issue until it parks: without this
				// reconcile would re-post the claim it just lost.
				_, err = worker.UpdateSlot(root, slot, func(s *worker.Slot) {
					if s.ClaimID() == claim {
						s.SetClaimID("")
					}
				})
				return err
			}
		}
		return nil
	}
}

func reportWarnings(c *Ctx, warnings []string) {
	for _, w := range warnings {
		c.Warn("%s", w)
	}
}
