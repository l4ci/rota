package round

import (
	"fmt"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/exitmap"
	"github.com/l4ci/rota/internal/milestone"
	"github.com/l4ci/rota/internal/worker"
)

// activeMilestoneIDs is the milestones that are active, read from be's store
// when it carries one and from the project's files otherwise.
func activeMilestoneIDs(root string, be backlog.Backend) ([]string, error) {
	var store milestone.Store = milestone.FileStore{Root: root}
	if b, ok := be.(interface{ MilestoneStore() milestone.Store }); ok {
		store = b.MilestoneStore()
	}
	list, err := store.List()
	if err != nil {
		return nil, err
	}
	return milestone.ActiveIDs(list), nil
}

// activeTags is the active milestones an item is tagged to.
func activeTags(it backlog.Item, active []string) []string {
	var out []string
	for _, m := range backlog.ParseMilestones(it.Fields.Get("milestone")) {
		if slices.Contains(active, m) {
			out = append(out, m)
		}
	}
	return out
}

// crossMilestone names what ties an item of an active milestone to another
// active milestone: a `## Depends on` issue tagged only to such a milestone,
// open or closed (closed work of an unshipped milestone sits on its branch, not
// the base), and an open issue of one that declares a `## Touches` entry the
// item declares too. Nothing is moved or resolved; the maintainer decides. An
// item outside every active milestone, or one whose dependencies all share its
// milestone or sit on the base, has none. active is activeMilestoneIDs; open is
// the open items of the board.
func (e Env) crossMilestone(be backlog.Backend, active []string, it backlog.Item, open []backlog.Item) ([]string, error) {
	mine := activeTags(it, active)
	if len(mine) == 0 {
		return nil, nil
	}
	other := func(x backlog.Item) []string {
		tags := activeTags(x, active)
		if len(tags) == 0 || slices.ContainsFunc(tags, func(t string) bool { return slices.Contains(mine, t) }) {
			return nil
		}
		return tags
	}
	from := strings.Join(mine, ",")
	body, _, _ := be.Detail(it.ID)
	var out []string
	refs, _ := dependencies(body, it.IssueKey().Project())
	for _, ref := range refs {
		if worker.NormID(ref) == worker.NormID(it.ID) {
			continue
		}
		dep, err := be.Get(ref)
		switch {
		case exitmap.IsNotFound(err):
			continue // the dependency check reports it
		case err != nil:
			return nil, err
		}
		if tags := other(*dep); len(tags) > 0 {
			state := "open"
			if dep.Closed {
				state = "merged on the milestone branch only"
			}
			out = append(out, fmt.Sprintf("%s (milestone %s) depends on %s of milestone %s (%s)", it.ID, from, dep.ID, strings.Join(tags, ","), state))
		}
	}
	myScopes := Scopes(body)
	if len(myScopes) == 0 {
		return out, nil
	}
	for _, x := range open {
		if worker.NormID(x.ID) == worker.NormID(it.ID) {
			continue
		}
		tags := other(x)
		if len(tags) == 0 {
			continue
		}
		xBody, _, _ := be.Detail(x.ID)
		if shared := ScopeOverlaps(myScopes, Scopes(xBody)); len(shared) > 0 {
			out = append(out, fmt.Sprintf("%s (milestone %s) touches %s with %s of milestone %s", it.ID, from, strings.Join(shared, ", "), x.ID, strings.Join(tags, ",")))
		}
	}
	return out, nil
}
