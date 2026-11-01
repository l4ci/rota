package worker

import (
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/verdict"
)

// stepVerdictReports runs only after the PR and its pushed head were validated.
// Both the blocking check and review-depth lookup then see the recovered cache.
func (runner gate) stepVerdictReports(result GateResult, target GateTarget, forged gateForgeResult, remote bool, refs gateRefsResult, verified string) (struct{}, GateResult, bool, error) {
	g := runner.operation(result, target, forged)
	g.remote = remote
	g.headRef, g.baseRef, g.verified = refs.head, refs.base, refs.localHead
	g.verified = verified
	finish := func(done bool, err error) (struct{}, GateResult, bool, error) {
		return struct{}{}, *g.res, done, err
	}

	if !g.remote || g.pr == "" || !backlog.CapabilitiesForSource(g.in.cfg.Tracker.Source).IssueIDs {
		return finish(false, nil)
	}
	source, ok := g.forge.(verdict.ReportSource)
	if !ok {
		return finish(false, nil)
	}
	pr, ok := PRRefNumber(g.pr)
	if !ok {
		return finish(false, nil)
	}
	head := strings.TrimSpace(g.verified)
	if head == "" {
		head, _ = g.git(g.root, "rev-parse", g.headRef)
		head = strings.TrimSpace(head)
	}
	if err := verdict.RecoverPR(g.ctx, g.root, verdict.BranchKey("", g.branch), head, pr, source); err != nil {
		g.res.Notes = append(g.res.Notes, fmt.Sprintf("VERDICT-REPORT-UNKNOWN %s — %v; using local verdicts", g.branch, err))
	}
	return finish(false, nil)
}
