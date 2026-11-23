package round

import (
	"context"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/verdict"
	"github.com/l4ci/rota/internal/worker"
	"strconv"
	"strings"
	"unicode"

	"github.com/l4ci/rota/internal/backlog"
)

// openPRIssues maps an issue number to the lowest-numbered open PR, keeping
// its head branch. The PR resolves it by a head `<agent>/<issue>-<slug>` or its body
// closes the issue (`Closes #N`). One OpenPRs call serves every issue. File
// mode and a missing forge have no open PRs to read, so the map is nil.
func (e Env) openPRIssues(ctx context.Context, be backlog.Backend) (map[tracker.IssueKey]tracker.PR, error) {
	return e.openPRIssuesExcept(ctx, be, "")
}

// openPRIssuesExcept is openPRIssues without the PR whose head is skip ("" for
// none): an attempt of a best-of:2 issue does not count its sibling's PR.
func (e Env) openPRIssuesExcept(ctx context.Context, be backlog.Backend, skip string) (map[tracker.IssueKey]tracker.PR, error) {
	prs, err := e.listOpenPRs(ctx, be)
	if err != nil || prs == nil {
		return nil, err
	}
	return e.issuesOfPRs(prs, skip), nil
}

// listOpenPRs is the forge's open PRs; nil in file mode or without a forge.
func (e Env) listOpenPRs(ctx context.Context, be backlog.Backend) ([]tracker.PR, error) {
	if !e.forgeOn(be) {
		return nil, nil
	}
	prs, err := e.Forge.OpenPRs(ctx)
	if err != nil {
		return nil, err
	}
	if prs == nil {
		prs = []tracker.PR{}
	}
	return prs, nil
}

// issuesOfPRs is openPRIssuesExcept over an already listed set of open PRs.
func (e Env) issuesOfPRs(prs []tracker.PR, skip string) map[tracker.IssueKey]tracker.PR {
	out := map[tracker.IssueKey]tracker.PR{}
	note := func(issue tracker.IssueKey, pr tracker.PR) {
		if cur, ok := out[issue]; !ok || pr.Number < cur.Number {
			out[issue] = pr
		}
	}
	for _, pr := range prs {
		if skip != "" && pr.Branch == skip {
			continue
		}
		if n, ok := worker.BranchIssue(pr.Branch); ok {
			note(n, pr)
		}
		for _, n := range e.Forge.ClosedNumbers(pr.Body) {
			note(tracker.NumKey(n), pr)
		}
	}
	return out
}

// deferredDependency is the open PR a --file-deferred item was filed from, 0
// when it is not such an item or that PR is no longer open. Another item that
// merely relates to an open PR is not held back.
//
// The hold is keyed on the open set, so a source PR closed without merging
// releases the item like a merged one (#1545). That is deliberate: no code will
// arrive for it, and holding forever would strand the item with no signal. It
// turns ready for a worker, who can dispute it if the finding is moot.
func deferredDependency(be backlog.Backend, it backlog.Item, open map[int]bool) int {
	if len(open) == 0 || it.Fields.Related == "" {
		return 0
	}
	body, _, _ := be.Detail(it.ID)
	if !verdict.IsDeferredFinding(body) {
		return 0
	}
	refs := strings.FieldsFunc(it.Fields.Related, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
	for _, ref := range refs {
		num, ok := tracker.PRRefNumber(ref)
		if !ok {
			continue
		}
		if n, _ := strconv.Atoi(num); open[n] {
			return n
		}
	}
	return 0
}
