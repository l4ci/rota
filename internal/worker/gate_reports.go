package worker

import (
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/verdict"
)

// stepVerdictReports runs only after the PR and its pushed head were validated.
// Both the blocking check and review-depth lookup then see the recovered cache.
func (g *gate) stepVerdictReports() (bool, error) {
	if !g.remote || g.pr == "" || !backlog.CapabilitiesForSource(g.in.cfg.Tracker.Source).IssueIDs {
		return false, nil
	}
	source, ok := g.forge.(verdict.ReportSource)
	if !ok {
		return false, nil
	}
	pr, ok := PRRefNumber(g.pr)
	if !ok {
		return false, nil
	}
	head := strings.TrimSpace(g.verified)
	if head == "" {
		head, _ = g.git(g.root, "rev-parse", g.headRef)
		head = strings.TrimSpace(head)
	}
	if err := verdict.RecoverPR(g.ctx, g.root, verdict.BranchKey("", g.branch), head, pr, source); err != nil {
		g.res.Notes = append(g.res.Notes, fmt.Sprintf("VERDICT-REPORT-UNKNOWN %s — %v; using local verdicts", g.branch, err))
	}
	return false, nil
}
