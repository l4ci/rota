package round

import (
	"context"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/milestone"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// Candidate is an item a round could assign, with its readiness.
type Candidate struct {
	ID, Title, Milestone string
	Readiness
}

// CandidateOpts selects the set.
type CandidateOpts struct {
	Scope string   // slate, milestone or next
	Slate []string // the approved items, for slate
	// Shared are the round.sharedPaths globs the overlap check ignores.
	Shared []string
}

// Candidates lists the open items the scope allows that no slot holds, in
// backlog order, each with its readiness. Scope slate is the slate and
// nothing else. Milestone is the items of the active milestones. Next is the
// same, and when none is left to assign, the items of the first planned
// milestone whose dependencies are all shipped.
func (e Env) Candidates(ctx context.Context, root string, be backlog.Backend, o CandidateOpts) ([]Candidate, error) {
	items, err := be.List(false)
	if err != nil {
		return nil, err
	}
	chosen, err := scopeSet(root, items, heldIDs(root), o.Scope, o.Slate)
	if err != nil {
		return nil, err
	}

	handed, err := e.handedToHuman(ctx, be)
	if err != nil {
		return nil, err
	}
	tracked := e.trackedFiles(ctx, root)
	inFlight := e.InFlightItems(ctx, root, be, tracked, o.Shared)
	var out []Candidate
	for _, it := range chosen {
		if it.Number != 0 && handed[it.Number] {
			continue
		}
		r, err := Assess(be, it.ID, tracked, o.Shared, inFlight, false)
		if err != nil {
			return nil, err
		}
		ms := backlog.ParseMilestones(it.Fields.Get("milestone"))
		c := Candidate{ID: it.ID, Title: it.Title, Readiness: r}
		if len(ms) > 0 {
			c.Milestone = strings.Join(ms, ",")
		}
		out = append(out, c)
	}
	return out, nil
}

func (e Env) trackedFiles(ctx context.Context, root string) []string {
	out, _, code, err := e.Git(ctx, root, "ls-files")
	if err != nil || code != 0 {
		return nil
	}
	var files []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			files = append(files, l)
		}
	}
	return files
}

// heldIDs are the items the registry's slots hold.
func heldIDs(root string) map[string]bool {
	held := map[string]bool{}
	for _, s := range worker.LoadRegistry(root).Slots() {
		if id := heldID(worker.Str(s, "task"), worker.Str(s, "branch"), worker.Str(s, "name")); id != "" {
			held[id] = true
		}
	}
	return held
}

// scopeSet is the open items of the scope that held does not name.
func scopeSet(root string, items []backlog.Item, held map[string]bool, scope string, slate []string) ([]backlog.Item, error) {
	pick := func(in func(backlog.Item) bool) []backlog.Item {
		var out []backlog.Item
		for _, it := range items {
			if !it.Closed && !held[it.ID] && in(it) {
				out = append(out, it)
			}
		}
		return out
	}
	inMilestones := func(ids []string) func(backlog.Item) bool {
		want := map[string]bool{}
		for _, id := range ids {
			want[id] = true
		}
		return func(it backlog.Item) bool {
			for _, m := range backlog.ParseMilestones(it.Fields.Get("milestone")) {
				if want[m] {
					return true
				}
			}
			return false
		}
	}
	var chosen []backlog.Item
	switch scope {
	case roundcfg.ScopeSlate:
		in := map[string]bool{}
		for _, id := range slate {
			in[strings.ToUpper(strings.TrimPrefix(id, "#"))] = true
		}
		chosen = pick(func(it backlog.Item) bool { return in[strings.ToUpper(it.ID)] })
	case roundcfg.ScopeMilestone, roundcfg.ScopeNext:
		active, err := milestone.Active(root)
		if err != nil {
			return nil, err
		}
		chosen = pick(inMilestones(active))
		if len(chosen) == 0 && scope == roundcfg.ScopeNext {
			all, err := milestone.List(root)
			if err != nil {
				return nil, err
			}
			for _, m := range all {
				if m.Status == "planned" && m.Ready {
					chosen = pick(inMilestones([]string{m.ID}))
					break
				}
			}
		}
	case roundcfg.ScopeOpen:
		chosen = pick(func(backlog.Item) bool { return true })
	default:
		return nil, &worker.Error{Exit: worker.ExitUsage, Message: "scope must be slate, milestone, next or open"}
	}
	return chosen, nil
}

// InScope reports whether the scope allows assigning id: it is a candidate
// now, or belongs to the scope's current set whether or not a slot holds it.
func InScope(root string, be backlog.Backend, scope string, slate []string, id string) (bool, error) {
	items, err := be.List(false)
	if err != nil {
		return false, err
	}
	for _, held := range []map[string]bool{heldIDs(root), nil} {
		set, err := scopeSet(root, items, held, scope, slate)
		if err != nil {
			return false, err
		}
		for _, it := range set {
			if strings.EqualFold(it.ID, id) {
				return true, nil
			}
		}
	}
	return false, nil
}

// handedToHuman is the open issues carrying the needs-human label (C10): the
// human holds them, so a round does not offer them. File mode has no labels.
func (e Env) handedToHuman(ctx context.Context, be backlog.Backend) (map[int]bool, error) {
	if e.Forge == nil || be.Name() != "issues" {
		return nil, nil
	}
	issues, err := e.Forge.List(ctx, tracker.ListFilter{State: "open", Labels: []string{firstNonEmpty(e.NeedsHuman, DefaultNeedsHuman)}})
	if err != nil {
		return nil, err
	}
	out := map[int]bool{}
	for _, is := range issues {
		out[is.Number] = true
	}
	return out, nil
}
