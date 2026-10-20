package round

import (
	"context"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/strutil"
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
	// Pick is the harness and model the item asks for; PickErr why its labels
	// are refused ("" when fine). assign refuses the same way.
	Pick    Pick
	PickErr string
	// Decision is the item's decision type ("" for an ordinary item); BlockedBy
	// names why a ready-looking item is still not assignable.
	Decision, BlockedBy string
	// ScopeSource is where the item's scope came from: touches, subsystem,
	// inferred or none. A none candidate wants a scout before it is assigned.
	ScopeSource string
	Readiness
}

// Ready is true when every readiness check holds and no open PR resolves the item.
func (c Candidate) Ready() bool {
	return c.OpenPR == 0 && c.PickErr == "" && c.BlockedBy == "" && c.Readiness.Ready()
}

// CandidateOpts selects the set.
type CandidateOpts struct {
	Scope string   // slate, milestone or next
	Slate []string // the approved items, for slate
	// Shared are the round.sharedPaths globs the overlap check ignores.
	Shared []string
	// ScopeOverlap is round.scopeOverlap: "block" fails the overlap check on a scope clash.
	ScopeOverlap string
}

// ScopeOf is the scope and slate the round's candidates are drawn from: an
// explicit override, else the scope the registry recorded, else round.scope.
// The slate is the recorded one whatever the scope came from. This is the only
// place the fallback is spelled.
func ScopeOf(root string, set roundcfg.Settings, override string) (scope string, slate []string) {
	scope, slate = SlateOf(root)
	if override != "" {
		scope = override
	}
	if scope == "" {
		scope = set.Scope
	}
	return scope, slate
}

// RoundCandidates is Candidates for the round as configured: the scope from
// ScopeOf, the shared paths and overlap mode from set. Callers pass only an
// explicit scope override ("" for none).
func (e Env) RoundCandidates(ctx context.Context, root string, be backlog.Backend, set roundcfg.Settings, override string) ([]Candidate, error) {
	scope, slate := ScopeOf(root, set, override)
	return e.Candidates(ctx, root, be, CandidateOpts{Scope: scope, Slate: slate, Shared: set.SharedPaths, ScopeOverlap: set.ScopeOverlap})
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
	unclear, err := e.labelled(ctx, be, NeedsInfoLabel)
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
		if handed[it.IssueKey()] {
			continue
		}
		if taken != nil && taken(it) {
			continue
		}
		r, err := AssessScoped(be, it.ID, tracked, o.Shared, inFlight, false, "", o.ScopeOverlap == "block")
		if err != nil {
			return nil, err
		}
		ms := backlog.ParseMilestones(it.Fields.Get("milestone"))
		_, src := ItemScopeSource(be, it.ID)
		c := Candidate{ID: it.ID, Title: it.Title, OpenPR: openPR[it.IssueKey()], ScopeSource: src, Readiness: r}
		if p, err := PickOf(be.Capabilities(), it); err != nil {
			c.PickErr = err.Error()
		} else {
			c.Pick = p
		}
		if d, err := DecisionOf(be.Capabilities(), it); err != nil {
			c.PickErr = err.Error()
		} else if c.Decision = d; d == DecisionGrilling {
			c.BlockedBy = BlockHumanDecision
		}
		if _, err := BestOfOf(be.Capabilities(), it); err != nil && c.PickErr == "" {
			c.PickErr = err.Error()
		}
		if unclear[it.IssueKey()] {
			c.Checks = append(c.Checks, Check{Name: NeedsInfoLabel, Detail: []string{"labelled " + NeedsInfoLabel + ": triage found it unclear, so it waits for the reporter"}})
		}
		if len(ms) > 0 {
			c.Milestone = strings.Join(ms, ",")
		}
		out = append(out, c)
	}
	return out, nil
}

func (e Env) trackedFiles(ctx context.Context, root string) []string {
	res, err := e.Git(ctx, root, "ls-files")
	out, code := res.Stdout, res.ExitCode
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
	imap, _ := fsio.LoadJSON(rotatree.IssueMap(root), nil).(*jsonx.Object)
	hold := func(id string) {
		if id == "" {
			return
		}
		held[worker.NormID(id)] = true
		if imap == nil {
			return
		}
		if e, ok := imap.Get(id); ok {
			if eo, ok := e.(*jsonx.Object); ok {
				if n, ok := eo.Get("number"); ok && n != nil {
					held[worker.NormID(fmt.Sprint(n))] = true
				}
			}
		}
	}
	reg := worker.LoadRegistryTolerant(root) // no error return; assign and adopt read the registry strictly before they claim
	for _, s := range reg.Slots() {
		hold(s.HeldID())
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
			if !it.Closed && !held[worker.NormID(it.ID)] && (in(it) || reviews[worker.NormID(it.ID)] && IsReviewTitle(it.Title)) {
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
			in[worker.NormID(id)] = true
		}
		chosen = pick(func(it backlog.Item) bool { return in[worker.NormID(it.ID)] })
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
		return nil, &exitcode.Error{Exit: exitcode.ExitUsage, Message: "scope must be slate, milestone, next or open"}
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
			if worker.NormID(it.ID) == worker.NormID(id) {
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
	if !e.forgeOn(be) {
		return nil, nil
	}
	issues, err := e.Forge.List(ctx, tracker.ListFilter{State: "open", Labels: []string{strutil.FirstNonEmpty(e.Label, DefaultLabel)}})
	if err != nil {
		return nil, err
	}
	labelled := map[tracker.IssueKey]bool{}
	for _, is := range issues {
		labelled[is.Key] = true
	}
	wf, _ := backlog.WorkflowOf(be)
	return func(it backlog.Item) bool {
		if labelled[it.IssueKey()] {
			return true
		}
		if wf == nil {
			return false
		}
		s, err := wf.Status(it.ID)
		return err == nil && s != nil && s.Claim != ""
	}, nil
}

// handedToHuman is the open issues carrying the needs-human label (C10): the
// human holds them, so a round does not offer them. File mode has no labels.
func (e Env) handedToHuman(ctx context.Context, be backlog.Backend) (map[tracker.IssueKey]bool, error) {
	return e.labelled(ctx, be, strutil.FirstNonEmpty(e.NeedsHuman, DefaultNeedsHuman))
}

// labelled is the open issues carrying label; nil without an issue tracker.
func (e Env) labelled(ctx context.Context, be backlog.Backend, label string) (map[tracker.IssueKey]bool, error) {
	if !e.forgeOn(be) {
		return nil, nil
	}
	issues, err := e.Forge.List(ctx, tracker.ListFilter{State: "open", Labels: []string{label}})
	if err != nil {
		return nil, err
	}
	out := map[tracker.IssueKey]bool{}
	for _, is := range issues {
		out[is.Key] = true
	}
	return out, nil
}
