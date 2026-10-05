package backlog

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/tracker"
)

// PRTracker is the part of tracker.Adapter the review queue and the PR merge
// need on top of Tracker: the open PRs and the merge.
type PRTracker interface {
	Tracker
	OpenPRs(ctx context.Context) ([]tracker.PR, error)
	ClosedNumbers(body string) []int
	PRMerge(ctx context.Context, pr int, o tracker.MergeOpts) (string, error)
	PRFiles(ctx context.Context, pr int) ([]string, error)
}

// MergeApprover is the gate MergePRGated runs once the PR and its items
// resolve, before the proof check: branch is the PR's head branch ("" when
// the forge reported none), for the verdict check (B3), and files lists the
// PR's changed paths on demand, for merge approval (B1). A non-nil error stops
// the merge with nothing changed.
type MergeApprover func(branch string, files func() ([]string, error)) error

// ReleaseTracker is the part of tracker.Adapter the release calls need: the
// native milestones and the issues in one.
type ReleaseTracker interface {
	MilestoneTracker
	IssuesInMilestone(ctx context.Context, title, state string) ([]tracker.Issue, error)
}

func (b *Issues) prTracker() (PRTracker, error) { return capability[PRTracker](b, noPRSupport) }

// ---- review queue ----------------------------------------------------------

// QueueEntry is one issue waiting for review and the open PRs that close it.
type QueueEntry struct {
	ID, Type string // the issue number ("<repo>:<number>" in an umbrella) and its type letter
	Number   int
	Title    string
	Repo     string // the sub-repo in an umbrella, else ""
	PRs      []tracker.PR
}

// ReviewQueue lists the open issues labelled needs-review with the open PRs
// whose body closes them, lowest number first (IssueBackend.review_queue). It
// makes one issue list and one PR list and matches in memory.
func (b *Issues) ReviewQueue() ([]QueueEntry, error) {
	pt, err := b.prTracker()
	if err != nil {
		return nil, err
	}
	label := config.Label(b.Cfg, "needsReview")
	list, err := pt.List(b.ctx(), tracker.ListFilter{State: "open", Labels: []string{label}})
	if err != nil {
		return nil, err
	}
	var issues []Issue
	for _, is := range list {
		if !b.IsMilestoneTracker(is) {
			issues = append(issues, is)
		}
	}
	prs, err := pt.OpenPRs(b.ctx())
	if err != nil {
		return nil, err
	}
	byIssue := map[int][]tracker.PR{}
	for _, pr := range prs {
		for _, n := range pt.ClosedNumbers(pr.Body) {
			byIssue[n] = append(byIssue[n], pr)
		}
	}
	sort.SliceStable(issues, func(i, j int) bool { return issues[i].Number < issues[j].Number })
	out := make([]QueueEntry, 0, len(issues))
	for _, is := range issues {
		linked := byIssue[is.Number]
		if linked == nil {
			linked = []tracker.PR{}
		}
		out = append(out, QueueEntry{ID: strconv.Itoa(is.Number), Type: b.Letter(is), Number: is.Number, Title: is.Title, PRs: linked})
	}
	return out, nil
}

// ---- PR merge --------------------------------------------------------------

// MergeFailedError is the forge refusing to merge a PR: the merge call itself
// failed with the CLI's own error. Nothing was merged.
type MergeFailedError struct {
	PR  int
	Err error
}

func (e *MergeFailedError) Error() string { return e.Err.Error() }
func (e *MergeFailedError) Unwrap() error { return e.Err }

// MergeResult is what MergePR did. SHA is empty and Unproven set when the
// proof gate stopped the merge.
type MergeResult struct {
	SHA      string
	Closed   []ItemRef
	Unproven []ItemRef
}

// ItemRef is an item as an issue ID plus its type letter.
type ItemRef struct{ ID, Type string }

// Ref is the old "F12" spelling.
func (r ItemRef) Ref() string { return r.Type + r.ID }

// MergePR merges PR pr and makes sure the items it links end up closed
// (IssueBackend.merge_pr). Linked is items when non-nil, else every issue the
// PR body closes. Proof is checked before merging, because a merge into the
// default branch lets the host close the issue and skip the gate: any open
// linked item without proof stops the merge, flips to changes-requested with a
// feedback comment, and the result lists it as Unproven. Otherwise the PR
// merges and the linked items the host left open close as done with the merge
// sha. An unknown PR (items nil only) or item wraps ErrNotFound; a refused
// merge is a *MergeFailedError.
func (b *Issues) MergePR(pr int, items []string) (MergeResult, error) {
	return b.MergePRGated(pr, items, nil)
}

// MergePRGated is MergePR with the B1 and B3 merge gates (nil: none).
func (b *Issues) MergePRGated(pr int, items []string, approve MergeApprover) (MergeResult, error) {
	pt, err := b.prTracker()
	if err != nil {
		return MergeResult{}, err
	}
	explicit := items != nil
	prs, err := pt.OpenPRs(b.ctx())
	if err != nil {
		return MergeResult{}, err
	}
	var found *tracker.PR
	for i := range prs {
		if prs[i].Number == pr {
			found = &prs[i]
			break
		}
	}
	if !explicit {
		if found == nil {
			return MergeResult{}, errf(ErrNotFound, "PR %d is not open", pr)
		}
		for _, n := range pt.ClosedNumbers(found.Body) {
			items = append(items, "#"+strconv.Itoa(n))
		}
	}
	var linked []string
	for _, ref := range items { // resolve before merging so a bad ref fails with nothing merged
		if explicit {
			_, id, err := b.require(ref)
			if err != nil {
				return MergeResult{}, err
			}
			linked = append(linked, id)
			continue
		}
		is, ok, err := b.lookup(ref) // a tracker issue is not an item
		if err != nil {
			return MergeResult{}, err
		}
		if ok {
			linked = append(linked, b.Letter(is)+strconv.Itoa(is.Number))
		}
	}
	// The contract makes a PR that is not open exit 3 with --items too. The
	// Python checked it only without them and let the forge refuse the merge;
	// checking here, after the items resolve, keeps its call order.
	if found == nil {
		return MergeResult{}, errf(ErrNotFound, "PR %d is not open", pr)
	}
	if approve != nil {
		if err := approve(found.Branch, func() ([]string, error) { return pt.PRFiles(b.ctx(), pr) }); err != nil {
			return MergeResult{}, err
		}
	}
	var unproven []string
	for _, ref := range linked {
		is, id, err := b.require(ref)
		if err != nil {
			return MergeResult{}, err
		}
		if is.State != "open" {
			continue
		}
		n, err := b.proofCount(id)
		if err != nil {
			return MergeResult{}, err
		}
		if n == 0 {
			unproven = append(unproven, id)
		}
	}
	if len(unproven) > 0 {
		for _, id := range unproven {
			if _, err := b.SetState(id, "changes-requested"); err != nil {
				return MergeResult{}, err
			}
			msg := "PR " + strconv.Itoa(pr) + " not merged: no proof recorded for " + id + ". " +
				"Add proof with rota proof add, then run the review again."
			if _, err := b.AddComment(id, "feedback", msg); err != nil {
				return MergeResult{}, err
			}
		}
		return MergeResult{Unproven: itemRefs(unproven)}, nil
	}
	// Unpinned: nothing here verified a head sha. The proof, verdict and
	// approval checks above key on the PR and its branch, not on a commit, so
	// there is no sha to pin to. The gate (internal/worker), which does verify
	// one, pins it.
	sha, err := pt.PRMerge(b.ctx(), pr, tracker.MergeOpts{DeleteBranch: true})
	if err != nil {
		var te *tracker.Error
		if errors.As(err, &te) && te.Kind == tracker.KindFailed && !strings.HasPrefix(te.Message, "cannot read the merge commit") {
			return MergeResult{}, &MergeFailedError{PR: pr, Err: err}
		}
		return MergeResult{}, err
	}
	var closed []string
	for _, ref := range linked {
		is, id, err := b.require(ref)
		if err != nil {
			return MergeResult{}, err
		}
		if is.State == "open" {
			if _, err := b.Complete(id, CompleteInput{Commit: shortSHA(sha), Reason: "done"}); err != nil {
				return MergeResult{}, err
			}
		} else if _, err := b.applyState(is, ""); err != nil { // the host closed it on merge; it leaves the state label behind
			return MergeResult{}, err
		}
		closed = append(closed, id)
	}
	return MergeResult{SHA: sha, Closed: itemRefs(closed)}, nil
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// itemRefs splits "F12" spellings into ID and type.
func itemRefs(ids []string) []ItemRef {
	out := make([]ItemRef, 0, len(ids))
	for _, id := range ids {
		out = append(out, ItemRef{ID: id[1:], Type: id[:1]})
	}
	return out
}

// ---- release ---------------------------------------------------------------

// releaseIssues is the issues of milestone mid's native milestone, tracking
// issue excluded, by number (_release_issues).
func (b *Issues) releaseIssues(mid string) ([]Issue, error) { return b.releaseIssuesOpt(mid, false) }

// releaseIssuesOpt is releaseIssues; with optional (an umbrella sub-repo
// other than home) the tracking issue may be absent.
func (b *Issues) releaseIssuesOpt(mid string, optional bool) ([]Issue, error) {
	rt, err := capability[ReleaseTracker](b, noMilestoneSupport)
	if err != nil {
		return nil, err
	}
	var tracking Issue
	if optional {
		all, err := b.trackingIssues()
		if err != nil {
			return nil, err
		}
		tracking = all[mid]
	} else if tracking, err = b.TrackerIssue(mid); err != nil {
		return nil, err
	}
	ms, err := b.nativeMilestone(rt, mid, tracking)
	if err != nil {
		return nil, err
	}
	if ms == nil {
		return nil, errf(ErrNotFound, "milestone %s has no native milestone on the issue tracker", mid)
	}
	all, err := rt.IssuesInMilestone(b.ctx(), ms.Title, "all")
	if err != nil {
		return nil, err
	}
	var out []Issue
	for _, is := range all {
		if (tracking.Number == 0 || is.Number != tracking.Number) && !b.IsMilestoneTracker(is) {
			out = append(out, is)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out, nil
}

// Blocker is an open issue that holds a release back, with the state label
// that does it.
type Blocker struct {
	Issue Issue
	Label string
}

// ReleaseGate lists the open issues of milestone mid: those carrying an
// in-progress, needs-review or changes-requested label block the release,
// every other open one is a warning (IssueBackend.release_gate).
func (b *Issues) ReleaseGate(mid string) (blocked []Blocker, warn []Issue, err error) {
	return b.releaseGate(mid, false)
}

func (b *Issues) releaseGate(mid string, optional bool) (blocked []Blocker, warn []Issue, err error) {
	issues, err := b.releaseIssuesOpt(mid, optional)
	if err != nil {
		return nil, nil, err
	}
	roles := b.stateLabels("inProgress", "needsReview", "changesRequested")
	for _, is := range issues {
		if is.State != "open" {
			continue
		}
		hit := ""
		for _, l := range roles {
			if has(is.Labels, l) {
				hit = l
				break
			}
		}
		if hit != "" {
			blocked = append(blocked, Blocker{is, hit})
		} else {
			warn = append(warn, is)
		}
	}
	return blocked, warn, nil
}

// NoteRow is one release-notes line: an issue title and number.
type NoteRow struct {
	Title  string
	Number int
}

// NoteSection is one release-notes heading and its rows.
type NoteSection struct {
	Name string
	Rows []NoteRow
}

// ReleaseNotes groups the issues of milestone mid that closed as completed:
// features under New, bugs under Fixed, the rest under Changed, always in that
// order (IssueBackend.release_notes).
func (b *Issues) ReleaseNotes(mid string) ([]NoteSection, error) { return b.releaseNotes(mid, false) }

func (b *Issues) releaseNotes(mid string, optional bool) ([]NoteSection, error) {
	issues, err := b.releaseIssuesOpt(mid, optional)
	if err != nil {
		return nil, err
	}
	out := []NoteSection{{Name: "New"}, {Name: "Fixed"}, {Name: "Changed"}}
	for _, is := range issues {
		if is.State != "closed" || is.StateReason != "completed" {
			continue
		}
		i := 2
		switch b.Letter(is) {
		case "F":
			i = 0
		case "B":
			i = 1
		}
		out[i].Rows = append(out[i].Rows, NoteRow{oneLine(is.Title), is.Number})
	}
	return out, nil
}

// writeCounter counts the writes that go through it.
type writeCounter struct {
	MilestoneTracker
	n int
}

func (w *writeCounter) Edit(ctx context.Context, number int, e tracker.IssueEdit) error {
	w.n++
	return w.MilestoneTracker.Edit(ctx, number, e)
}

func (w *writeCounter) AddLabels(ctx context.Context, number int, labels []string, auto bool) error {
	w.n++
	return w.MilestoneTracker.AddLabels(ctx, number, labels, auto)
}

func (w *writeCounter) RemoveLabels(ctx context.Context, number int, labels []string) error {
	w.n++
	return w.MilestoneTracker.RemoveLabels(ctx, number, labels)
}

func (w *writeCounter) Close(ctx context.Context, number int, reason, comment string) error {
	w.n++
	return w.MilestoneTracker.Close(ctx, number, reason, comment)
}

func (w *writeCounter) Reopen(ctx context.Context, number int) error {
	w.n++
	return w.MilestoneTracker.Reopen(ctx, number)
}

func (w *writeCounter) AddComment(ctx context.Context, number int, body string) (string, error) {
	w.n++
	return w.MilestoneTracker.AddComment(ctx, number, body)
}

func (w *writeCounter) EditMilestone(ctx context.Context, number int, e tracker.MilestoneEdit) error {
	w.n++
	return w.MilestoneTracker.EditMilestone(ctx, number, e)
}

type releaseTrackerCounter struct {
	*writeCounter
	rt ReleaseTracker
}

func (r releaseTrackerCounter) IssuesInMilestone(ctx context.Context, title, state string) ([]tracker.Issue, error) {
	return r.rt.IssuesInMilestone(ctx, title, state)
}

// ReleaseClose labels `released` and comments `Released in <tag>` on each
// completed issue of milestone mid (skipping what is there), then closes the
// native milestone and marks it shipped (IssueBackend.release_close). It
// returns the number of completed issues, and whether it wrote anything: a
// second run on a closed-out milestone writes nothing.
func (b *Issues) ReleaseClose(mid, tag string) (issues int, changed bool, err error) {
	cp, wc, err := b.counting()
	if err != nil {
		return 0, false, err
	}
	if issues, err = cp.labelReleased(mid, tag, false); err != nil {
		return issues, wc.n > 0, err
	}
	err = cp.MilestoneStatus(mid, "shipped")
	return issues, wc.n > 0, err
}

// counting is a copy of b whose tracker counts the writes made through it.
func (b *Issues) counting() (*Issues, *writeCounter, error) {
	rt, err := capability[ReleaseTracker](b, noMilestoneSupport)
	if err != nil {
		return nil, nil, err
	}
	wc := &writeCounter{MilestoneTracker: rt}
	cp := *b
	cp.Tracker = releaseTrackerCounter{wc, rt}
	return &cp, wc, nil
}

// labelReleased labels and comments the completed issues of mid
// (_label_released) and returns how many there are.
func (b *Issues) labelReleased(mid, tag string, optional bool) (issues int, err error) {
	all, err := b.releaseIssuesOpt(mid, optional)
	if err != nil {
		return 0, err
	}
	label := config.Label(b.Cfg, "released")
	// The marker keeps the comment from reading as a human's answer to an
	// escalation; a comment posted before markers existed still counts as seen.
	text := "Released in " + tag
	body := text + "\n\n" + marker.Line("released")
	for _, is := range all {
		if is.State != "closed" || is.StateReason != "completed" {
			continue
		}
		issues++
		if !has(is.Labels, label) {
			if err := b.Tracker.AddLabels(b.ctx(), is.Number, []string{label}, b.autoCreate()); err != nil {
				return issues, err
			}
		}
		comments, err := b.Tracker.Comments(b.ctx(), is.Number)
		if err != nil {
			return issues, err
		}
		seen := false
		for _, c := range comments {
			got := pystr.Strip(c.Body)
			seen = seen || got == text || got == pystr.Strip(body)
		}
		if !seen {
			if _, err := b.Tracker.AddComment(b.ctx(), is.Number, body); err != nil {
				return issues, err
			}
		}
	}
	return issues, nil
}

// ---- umbrella --------------------------------------------------------------

// perRepo is the sub-repo a per-repo umbrella verb acts on: Scope (--repo),
// else the one the working directory is in (contract: scope S). At the
// umbrella root it needs --repo (UmbrellaBackend._sub).
func (u *Umbrella) perRepo() (string, *Issues, error) {
	name := u.readScope()
	if name == "" {
		return "", nil, errf(ErrInvalid, "umbrella issue mode needs --repo <name> (registered: %s)", u.names())
	}
	s, err := u.sub(name)
	return name, s, err
}

// ReviewQueue is the review queue of every sub-repo in scope, in registry
// order, with IDs as "<repo>:<number>".
func (u *Umbrella) ReviewQueue() ([]QueueEntry, error) {
	out := []QueueEntry{}
	err := u.eachScoped(func(name string, s *Issues) error {
		q, err := s.ReviewQueue()
		for _, e := range q {
			e.ID, e.Repo = qualifyID(name, e.ID), name
			out = append(out, e)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// MergePR merges PR pr of the --repo sub-repo. An --items reference qualified
// with another sub-repo is an error; the IDs it reports are qualified.
func (u *Umbrella) MergePR(pr int, items []string) (MergeResult, error) {
	return u.MergePRGated(pr, items, nil)
}

// MergePRGated is MergePR with the merge-approval gate (nil: none).
func (u *Umbrella) MergePRGated(pr int, items []string, approve MergeApprover) (MergeResult, error) {
	name, s, err := u.perRepo()
	if err != nil {
		return MergeResult{}, err
	}
	var plain []string
	for _, ref := range items {
		owner, p, err := u.pick(ref)
		if err != nil {
			return MergeResult{}, err
		}
		if owner != nil && owner.Repo != name {
			return MergeResult{}, errf(ErrInvalid, "item %s belongs to %s, not %s", ref, owner.Repo, name)
		}
		plain = append(plain, p)
	}
	if items == nil {
		plain = nil
	}
	res, err := s.MergePRGated(pr, plain, approve)
	for i := range res.Closed {
		res.Closed[i].ID = qualifyID(name, res.Closed[i].ID)
	}
	for i := range res.Unproven {
		res.Unproven[i].ID = qualifyID(name, res.Unproven[i].ID)
	}
	return res, err
}

// ReleaseGate is the release gate of mid in the --repo sub-repo, whose
// tracking issue may live in the home sub-repo.
func (u *Umbrella) ReleaseGate(mid string) ([]Blocker, []Issue, error) {
	type gate struct {
		blocked []Blocker
		warn    []Issue
	}
	g, err := viaRepo(u, func(_ string, s *Issues) (gate, error) {
		blocked, warn, err := s.releaseGate(mid, true)
		return gate{blocked, warn}, err
	})
	return g.blocked, g.warn, err
}

// ReleaseNotes are the release notes of mid in the --repo sub-repo.
func (u *Umbrella) ReleaseNotes(mid string) ([]NoteSection, error) {
	return viaRepo(u, func(_ string, s *Issues) ([]NoteSection, error) { return s.releaseNotes(mid, true) })
}

// natives maps each sub-repo that has milestone mid to its native milestone,
// open ones first (UmbrellaBackend._natives).
func (u *Umbrella) natives(mid string) (map[string]*tracker.Milestone, error) {
	out := map[string]*tracker.Milestone{}
	err := u.eachRepo(func(name string, s *Issues) error {
		mt, err := s.milestoneTracker()
		if err != nil {
			return err
		}
		nm, err := s.nativeMilestone(mt, mid, Issue{})
		if nm != nil {
			out[name] = nm
		}
		return err
	})
	return out, err
}

// ReleaseClose closes out mid in the --repo sub-repo: it labels and comments
// the completed issues and closes that sub-repo's native milestone. The
// tracking issue ships (with every other native milestone closed) only once
// every sub-repo's native milestone is closed.
func (u *Umbrella) ReleaseClose(mid, tag string) (issues int, changed bool, err error) {
	name, s, err := u.perRepo()
	if err != nil {
		return 0, false, err
	}
	cp, wc, err := s.counting()
	if err != nil {
		return 0, false, err
	}
	if issues, err = cp.labelReleased(mid, tag, true); err != nil {
		return issues, wc.n > 0, err
	}
	// natives reads every sub-repo, so one whose forge fails stops the close
	// after the labels and comments went out. That is safe: they are skipped
	// when present, so a re-run picks up where this one stopped.
	nat, err := u.natives(mid)
	if err != nil {
		return issues, wc.n > 0, err
	}
	if ms := nat[name]; ms != nil && ms.State != "closed" {
		closed := "closed"
		if err := wc.EditMilestone(u.ctx(), ms.Number, tracker.MilestoneEdit{State: &closed}); err != nil {
			return issues, true, err
		}
		ms.State = "closed"
	}
	for _, ms := range nat {
		if ms.State != "closed" {
			return issues, wc.n > 0, nil
		}
	}
	shipped, err := u.milestoneShipped(mid)
	return issues, wc.n > 0 || shipped, err
}

// milestoneShipped marks mid shipped on the home tracking issue
// (UmbrellaBackend.milestone_status with "shipped", called once every native
// milestone is closed, so no other sub-repo's milestone is left to close). It
// reports whether it wrote anything.
func (u *Umbrella) milestoneShipped(mid string) (bool, error) {
	return viaHome(u, func(home *Issues) (bool, error) {
		hc, hw, err := home.counting()
		if err != nil {
			return false, err
		}
		err = hc.MilestoneStatus(mid, "shipped")
		return hw.n > 0, err
	})
}
