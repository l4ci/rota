package round

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/milestone"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// Candidate is an item a round could assign, with its readiness.
type Candidate struct {
	ID, Title, Milestone string
	// OpenPR is the open PR that already resolves the item, 0 for none: the
	// item is not ready while one is open.
	OpenPR int
	Readiness
}

// Ready is true when every readiness check holds and no open PR resolves the item.
func (c Candidate) Ready() bool { return c.OpenPR == 0 && c.Readiness.Ready() }

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
	var taken func(backlog.Item) bool
	if o.Scope == roundcfg.ScopeOpen {
		if taken, err = e.takenOutside(ctx, be); err != nil {
			return nil, err
		}
	}
	openPR, err := e.openPRIssues(ctx, be)
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
		if taken != nil && taken(it) {
			continue
		}
		r, err := Assess(be, it.ID, tracked, o.Shared, inFlight, false)
		if err != nil {
			return nil, err
		}
		ms := backlog.ParseMilestones(it.Fields.Get("milestone"))
		c := Candidate{ID: it.ID, Title: it.Title, OpenPR: openPR[it.Number], Readiness: r}
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

// heldIDs are the items the registry's slots hold, and those a queued PR holds.
// An ID a mid-round `migrate issues` left in file spelling (`B31`) also holds
// the issue the map gives it.
func heldIDs(root string) map[string]bool {
	held := map[string]bool{}
	imap, _ := fsio.LoadJSON(filepath.Join(root, ".rota", "issue-map.json"), nil).(*jsonx.Object)
	hold := func(id string) {
		if id == "" {
			return
		}
		held[id] = true
		if imap == nil {
			return
		}
		if e, ok := imap.Get(id); ok {
			if eo, ok := e.(*jsonx.Object); ok {
				if n, ok := eo.Get("number"); ok && n != nil {
					held[fmt.Sprint(n)] = true
				}
			}
		}
	}
	reg := worker.LoadRegistry(root)
	for _, s := range reg.Slots() {
		hold(heldID(worker.Str(s, "task"), worker.Str(s, "branch"), worker.Str(s, "name")))
	}
	for _, q := range reg.PRs() {
		hold(queuedIssue(q))
	}
	return held
}

// scopeSet is the open items of the scope that held does not name.
func scopeSet(root string, items []backlog.Item, held map[string]bool, scope string, slate []string) ([]backlog.Item, error) {
	reviews := MintedReviews(root)
	pick := func(in func(backlog.Item) bool) []backlog.Item {
		var out []backlog.Item
		for _, it := range items {
			// A review item is the round's own work: every scope offers it.
			if !it.Closed && !held[it.ID] && (in(it) || reviews[strings.ToUpper(it.ID)] && IsReviewTitle(it.Title)) {
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
		active, planned, fellBack, err := milestoneScope(root, scope)
		if err != nil {
			return nil, err
		}
		chosen = pick(inMilestones(active))
		if len(chosen) == 0 && len(planned) > 0 {
			chosen = pick(inMilestones(planned))
		}
		if fellBack {
			chosen = pick(func(backlog.Item) bool { return true })
		}
	case roundcfg.ScopeOpen:
		chosen = pick(func(backlog.Item) bool { return true })
	default:
		return nil, &worker.Error{Exit: worker.ExitUsage, Message: "scope must be slate, milestone, next or open"}
	}
	return chosen, nil
}

// milestoneScope is where scope milestone or next draws from: the active
// milestones and, for next, the first ready planned one. fellBack is true when
// the project has no unfinished milestone at all (none written, or all shipped
// or archived): the round then offers every open item, as scope open does,
// instead of an empty list. A project that still has a planned milestone keeps
// the strict scope, so milestone never rolls over. It reads milestone state
// alone, so candidates and assign agree.
func milestoneScope(root, scope string) (active, planned []string, fellBack bool, err error) {
	if active, err = milestone.Active(root); err != nil {
		return nil, nil, false, err
	}
	all, err := milestone.List(root)
	if err != nil {
		return nil, nil, false, err
	}
	pending := len(active) > 0
	for _, m := range all {
		if m.Status == "planned" || m.Status == "active" {
			pending = true
		}
		if scope == roundcfg.ScopeNext && planned == nil && m.Status == "planned" && m.Ready {
			planned = []string{m.ID}
		}
	}
	return active, planned, !pending, nil
}

// FellBack reports whether scope milestone or next has no unfinished
// milestone to draw from, so the round offers every open item.
func FellBack(root, scope string) bool {
	if scope != roundcfg.ScopeMilestone && scope != roundcfg.ScopeNext {
		return false
	}
	_, _, fb, err := milestoneScope(root, scope)
	return err == nil && fb
}

// Empty says why a scope offers nothing and what to run next.
type Empty struct {
	Reason, Next string
}

// WhyEmpty explains an empty candidate list: the backlog has no open items,
// the scope selects none, or everything the scope selects is held, in review,
// taken or handed to a human. Next is the command that moves the round on.
func (e Env) WhyEmpty(ctx context.Context, root string, be backlog.Backend, scope string, slate []string) (Empty, error) {
	items, err := be.List(false)
	if err != nil {
		return Empty{}, err
	}
	open := 0
	for _, it := range items {
		if !it.Closed {
			open++
		}
	}
	if open == 0 {
		return Empty{"the backlog has no open items", "capture work with /rota-capture, then run rota round candidates"}, nil
	}
	inScope, err := scopeSet(root, items, nil, scope, slate)
	if err != nil {
		return Empty{}, err
	}
	if len(inScope) == 0 {
		switch scope {
		case roundcfg.ScopeSlate:
			return Empty{"none of the slate items is open", "rota round start --scope slate --items <ID>[,<ID>…], or --scope open"}, nil
		default:
			return Empty{fmt.Sprintf("scope %s has no open items (%d open in the backlog)", scope, open), "rota round start --scope open"}, nil
		}
	}
	return Empty{fmt.Sprintf("all %d open items in scope %s are held by a slot, in review, taken or handed to a human", len(inScope), scope), "rota round status"}, nil
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

// takenOutside says whether an item someone outside the round has taken: it
// carries the in-progress label or an open claim while no slot or PR in
// review holds it (scopeSet already dropped those). Scope open offers every
// open item, so without this a hand-worked issue reads as ready and assign
// refuses it as claimed. File mode has neither labels nor claims.
func (e Env) takenOutside(ctx context.Context, be backlog.Backend) (func(backlog.Item) bool, error) {
	if e.Forge == nil || be.Name() != "issues" {
		return nil, nil
	}
	issues, err := e.Forge.List(ctx, tracker.ListFilter{State: "open", Labels: []string{firstNonEmpty(e.Label, DefaultLabel)}})
	if err != nil {
		return nil, err
	}
	labelled := map[int]bool{}
	for _, is := range issues {
		labelled[is.Number] = true
	}
	st, _ := be.(interface {
		Status(string) (*backlog.Status, error)
	})
	return func(it backlog.Item) bool {
		if labelled[it.Number] {
			return true
		}
		if st == nil {
			return false
		}
		s, err := st.Status(it.ID)
		return err == nil && s != nil && s.Claim != ""
	}, nil
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
