package round

import (
	"context"
	"strconv"

	"github.com/l4ci/rota/internal/backlog"
)

// openPRIssues maps an issue number to the lowest-numbered open PR that
// resolves it: the PR's head branch is `<agent>/<issue>-<slug>` or its body
// closes the issue (`Closes #N`). One OpenPRs call serves every issue. File
// mode and a missing forge have no open PRs to read, so the map is nil.
func (e Env) openPRIssues(ctx context.Context, be backlog.Backend) (map[int]int, error) {
	if e.Forge == nil || !be.Capabilities().Tracker {
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
		if m := reIssueBranch.FindStringSubmatch(pr.Branch); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil {
				note(n, pr.Number)
			}
		}
		for _, n := range e.Forge.ClosedNumbers(pr.Body) {
			note(n, pr.Number)
		}
	}
	return out, nil
}
