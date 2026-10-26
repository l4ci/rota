package cli

import (
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/postgate"
	"github.com/l4ci/rota/internal/verdict"
	"github.com/l4ci/rota/internal/worker"
)

// postGateReport runs before the bookkeeping's early refusal return: refusing
// a merge is as much a recorded gate outcome as landing one. It never changes
// the outcome, and a check-only invocation never calls it.
func postGateReport(c *Ctx, root string, r worker.GateResult) {
	if r.Branch == "" || r.Verdict == "" {
		return
	}
	// A recorded PR with no origin is a gate refusal with zero forge traffic.
	// Reporting must preserve that boundary even with an explicit provider.
	if r.PR != "" {
		remote, err := c.deps().Repo(root).Run(c.Context(), "remote", "get-url", "origin")
		if err != nil || remote.ExitCode != 0 {
			return
		}
	}
	ref := r.Branch
	if ok, _ := c.deps().Repo(root).Verify(c.Context(), "refs/heads/"+ref); !ok {
		ref = "origin/" + ref
	}
	if r.SHA == "" {
		if sha, err := reviewGit(c.Context(), c.deps(), root, "rev-parse", "--short", ref); err == nil {
			r.SHA = strings.TrimSpace(sha)
		}
	}
	if r.SHA == "" {
		c.Warn("gate report pending: cannot resolve sha for %s", r.Branch)
		return
	}
	t := verdictBranch{root: root, branch: r.Branch, ref: ref, dir: root}
	var items []verdict.ItemVerdict
	if r.Issue != "" {
		items = append(items, verdict.ItemVerdict{ID: r.Issue})
	}
	if err := resolveReportTargets(c, t, r.PR, items).post(c, postgate.RenderReport(r)); err != nil {
		c.Warn("gate report pending: %s", err)
	}
}

// A train's landed members and culprit are the PRs whose gate outcomes changed.
// Unattempted members receive no outcome report.
func postTrainReports(c *Ctx, root string, issues map[string]string, r worker.TrainResult) {
	for _, m := range r.Members {
		if !m.Landed {
			continue
		}
		checks := append(append([]string{}, r.Verified...), r.E2EVerified...)
		postGateReport(c, root, worker.GateResult{Branch: m.Branch, PR: m.PR, Issue: issues[m.Target], SHA: r.SHA, Verdict: worker.GatePass, Verified: checks, VerifySkipped: len(checks) == 0})
	}
	if r.Culprit != "" {
		g := r.CulpritGate
		if g.Branch == "" {
			for _, m := range r.Members {
				if m.Target == r.Culprit {
					g.Branch, g.PR = m.Branch, m.PR
					break
				}
			}
		}
		g.Issue = issues[r.Culprit]
		g.Verdict, g.Err, g.Hint = r.Verdict, r.Err, r.Hint
		if g.Err == "" {
			g.Err = fmt.Sprintf("train refused: %s", r.Verdict)
		}
		postGateReport(c, root, g)
	}
}
