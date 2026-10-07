package round

import (
	"context"
	"github.com/l4ci/rota/internal/worker"

	"github.com/l4ci/rota/internal/backlog"
)

// openPRIssues maps an issue number to the lowest-numbered open PR that
// resolves it: the PR's head branch is `<agent>/<issue>-<slug>` or its body
// closes the issue (`Closes #N`). One OpenPRs call serves every issue. File
// mode and a missing forge have no open PRs to read, so the map is nil.
func (e Env) openPRIssues(ctx context.Context, be backlog.Backend) (map[int]int, error) {
	return e.openPRIssuesExcept(ctx, be, "")
}

// openPRIssuesExcept is openPRIssues without the PR whose head is skip ("" for
// none): an attempt of a best-of:2 issue does not count its sibling's PR.
func (e Env) openPRIssuesExcept(ctx context.Context, be backlog.Backend, skip string) (map[int]int, error) {
	if !e.forgeOn(be) {
		return nil, nil
	}
	prs, err := e.Forge.OpenPRs(ctx)
	if err != nil {
		return nil, err
	}
	out := map[int]int{}
	note := func(issue, pr int) {
		if cur, ok := out[issue]; !ok || pr < cur {
			out[issue] = pr
		}
	}
	for _, pr := range prs {
		if skip != "" && pr.Branch == skip {
			continue
		}
		if n, ok := worker.BranchIssue(pr.Branch); ok {
			note(n, pr.Number)
		}
		for _, n := range e.Forge.ClosedNumbers(pr.Body) {
			note(n, pr.Number)
		}
	}
	return out, nil
}
