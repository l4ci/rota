package round

import (
	"fmt"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/exitmap"
	"github.com/l4ci/rota/internal/labels"
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

// crossMilestones shares lazy milestone and open-item reads across a candidates
// pass or an assignment (including bundle members).
type crossMilestones struct {
	root       string
	be         backlog.Backend
	active     []string
	activeRead bool
	open       []backlog.Item
	openRead   bool
	bodies     map[string]crossMilestoneBody
}

type crossMilestoneBody struct {
	text   string
	scopes []string
}

// body caches parsed scopes too, so comparing each candidate with the same
// other-milestone peers does not multiply tracker reads or parsing.
func (c *crossMilestones) body(id string) (crossMilestoneBody, error) {
	if b, ok := c.bodies[id]; ok {
		return b, nil
	}
	text, _, err := c.be.Detail(id)
	if err != nil && !exitmap.IsNotFound(err) {
		return crossMilestoneBody{}, err
	}
	if exitmap.IsNotFound(err) {
		text = ""
	}
	b := crossMilestoneBody{text: text, scopes: Scopes(text)}
	if c.bodies == nil {
		c.bodies = map[string]crossMilestoneBody{}
	}
	c.bodies[id] = b
	return b, nil
}

func (c *crossMilestones) loadActive() error {
	if c.activeRead {
		return nil
	}
	var err error
	c.active, err = activeMilestoneIDs(c.root, c.be)
	c.activeRead = err == nil
	return err
}

// check names dependencies and shared touches tying an item to another active
// milestone. Closed work counts only by its on-milestone/<id> labels, never by
// its milestone tag: it may have landed on main before a branch was cut.
func (c *crossMilestones) check(it backlog.Item) ([]string, error) {
	body, err := c.body(it.ID)
	if err != nil {
		return nil, err
	}
	from := func() string {
		if mine := activeTags(it, c.active); len(mine) > 0 {
			return "milestone " + strings.Join(mine, ",")
		}
		return "main"
	}
	other := func(tags []string) ([]string, error) {
		if len(tags) == 0 {
			return nil, nil
		}
		if err := c.loadActive(); err != nil {
			return nil, err
		}
		mine := activeTags(it, c.active)
		tags = slices.DeleteFunc(tags, func(t string) bool { return !slices.Contains(c.active, t) })
		if slices.ContainsFunc(tags, func(t string) bool { return slices.Contains(mine, t) }) {
			return nil, nil
		}
		return tags, nil
	}
	var out []string
	refs, _ := dependencies(body.text, it.IssueKey().Project())
	for _, ref := range refs {
		if worker.NormID(ref) == worker.NormID(it.ID) {
			continue
		}
		dep, err := c.be.Get(ref)
		if exitmap.IsNotFound(err) {
			continue // readiness reports missing dependencies
		}
		if err != nil {
			return nil, err
		}
		tags := backlog.ParseMilestones(dep.Fields.Get("milestone"))
		state := "open"
		if dep.Closed {
			tags = nil
			for _, label := range dep.Labels {
				if id, ok := strings.CutPrefix(label, labels.OnMilestonePrefix); ok && id != "" {
					tags = append(tags, id)
				}
			}
			state = "merged on the milestone branch only"
		}
		tags, err = other(tags)
		if err != nil {
			return nil, err
		}
		if len(tags) > 0 {
			out = append(out, fmt.Sprintf("%s (%s) depends on %s of milestone %s (%s)", it.ID, from(), dep.ID, strings.Join(tags, ","), state))
		}
	}
	myScopes := body.scopes
	if len(myScopes) == 0 || len(backlog.ParseMilestones(it.Fields.Get("milestone"))) == 0 {
		return out, nil
	}
	if err := c.loadActive(); err != nil {
		return nil, err
	}
	if len(activeTags(it, c.active)) == 0 {
		return out, nil
	}
	if !c.openRead {
		c.open, err = c.be.List(false)
		if err != nil {
			return nil, err
		}
		c.openRead = true
	}
	for _, x := range c.open {
		if worker.NormID(x.ID) == worker.NormID(it.ID) {
			continue
		}
		tags, err := other(backlog.ParseMilestones(x.Fields.Get("milestone")))
		if err != nil {
			return nil, err
		}
		if len(tags) == 0 {
			continue
		}
		xBody, err := c.body(x.ID)
		if err != nil {
			return nil, err
		}
		if shared := ScopeOverlaps(myScopes, xBody.scopes); len(shared) > 0 {
			out = append(out, fmt.Sprintf("%s (%s) touches %s with %s of milestone %s", it.ID, from(), strings.Join(shared, ", "), x.ID, strings.Join(tags, ",")))
		}
	}
	return out, nil
}
