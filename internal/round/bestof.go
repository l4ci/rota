package round

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/worker"
)

// BestOfLabel is the issue label that has two slots build the issue at once.
const BestOfLabel = "best-of:2"

// Blocked reasons of a best-of:2 issue (exit 4, `blockedBy`).
const (
	BlockBestOfLabel = "best-of label"
	BlockBestOfSlots = "best-of-slots"
	BlockBestOf      = "best-of"
)

// KindFromBestOf is the kind source of a best-of:2 attempt that took the
// harness its sibling did not.
const KindFromBestOf = "best-of"

// BestOfOf reports whether an item is built best-of:2. Only issues carry
// labels; a best-of: label rota does not know, or two of them, is a refusal
// naming the label.
func BestOfOf(caps backlog.Capabilities, it backlog.Item) (bool, error) {
	if !caps.IssueIDs {
		return false, nil
	}
	ls := labelsWith(it.Labels, "best-of:")
	switch {
	case len(ls) == 0:
		return false, nil
	case len(ls) == 1 && ls[0] == BestOfLabel:
		return true, nil
	case len(ls) == 1:
		return false, blocked(BlockBestOfLabel, "%s has label %s: only %s is supported", it.ID, ls[0], BestOfLabel)
	}
	return false, blocked(BlockBestOfLabel, "%s has labels %s: only %s is supported", it.ID, strings.Join(ls, ", "), BestOfLabel)
}

// bestOfRun is what one attempt of a best-of:2 issue knows beyond a plain
// assignment: its sibling, the kind its assignBestOf settled, and whether this
// run only checks that the attempt could start.
type bestOfRun struct {
	sibling, siblingBranch string
	kind, kindSource       string
	preflight              bool
}

// inReview is the queued PR record that keeps id from being assigned: the
// first one, except that an attempt of a best-of:2 issue ignores its sibling's.
func inReview(reg worker.Registry, id string, bo *bestOfRun) *worker.QueuedPR {
	if bo == nil {
		return reg.QueuedIssue(id)
	}
	want := strings.ToUpper(strings.TrimPrefix(strings.TrimSpace(id), "#"))
	for _, q := range reg.PRs() {
		if queuedIssue(q) == want && q.From != bo.sibling {
			return &q
		}
	}
	return nil
}

// assignBestOf binds a best-of:2 issue to two slots, one attempt each, each
// with its own claim, branch and smoke number. The second attempt takes the
// other harness kind when both have a tier map and nothing pins the kind.
// Both attempts are checked before the first is marked, so a refusal changes
// nothing; the registry record goes in before the first attempt, so a crash in
// between leaves something `round pick` and the gate can read.
func (e Env) assignBestOf(ctx context.Context, root string, be Board, o AssignOpts, it *backlog.Item) (res Assigned, err error) {
	set := o.Settings
	id := it.ID
	res.ID, res.Type = id, it.Type
	if _, ok := be.(backlog.SharedClaimer); !ok {
		return res, usage("%s is best-of:2, but this backlog cannot hold two claims", id)
	}
	if err := o.checkAgent(); err != nil {
		return res, err
	}
	if err := e.requireLease(ctx, root, o); err != nil {
		return res, err
	}

	reg, err := worker.LoadRegistry(root)
	if err != nil {
		return res, err
	}
	rec := reg.BestOf(id)
	want := strings.ToUpper(id)
	var slots []*worker.Slot
	add := func(s *worker.Slot) {
		if s != nil && len(slots) < 2 && !slices.ContainsFunc(slots, func(p *worker.Slot) bool { return p.Name() == s.Name() }) {
			slots = append(slots, s)
		}
	}
	if o.Agent != "" {
		s := reg.Slot(o.Agent)
		if s == nil {
			return res, &exitcode.Error{Exit: exitcode.ExitResolution, Message: fmt.Sprintf("slot %s is not provisioned: run rota round start", o.Agent)}
		}
		add(s)
	}
	// Slots that hold the issue resume (the record's order first), then idle
	// ones, then those whose PR can be queued: the order single assign uses.
	if rec != nil {
		for _, a := range rec.Attempts {
			if s := reg.Slot(a.Slot); s != nil && s.HeldID() == want {
				add(s)
			}
		}
	}
	for _, name := range set.Roster {
		if s := reg.Slot(name); s != nil && s.HeldID() == want {
			add(s)
		}
	}
	for _, name := range set.Roster {
		if s := reg.Slot(name); s != nil && s.HeldID() == "" {
			add(s)
		}
	}
	for _, name := range set.Roster {
		if len(slots) == 2 {
			break
		}
		if s := reg.Slot(name); s != nil {
			if ok, _ := e.parkable(ctx, s); ok {
				add(s)
			}
		}
	}
	if len(slots) < 2 {
		return res, blocked(BlockBestOfSlots, "%s is best-of:2 and needs two free slots; %d free", id, len(slots))
	}

	pick, err := PickOf(be.Capabilities(), *it)
	if err != nil {
		return res, err
	}
	kinds, sources := [2]string{}, [2]string{}
	kinds[0], sources[0] = resolveKind(o.Kind, pick.Harness, set.WorkerKind, slots[0].HarnessKind())
	kinds[1], sources[1] = kinds[0], sources[0]
	pinned := o.Kind != "" || pick.Harness != ""
	if !pinned && len(set.Models[harness.Claude]) > 0 && len(set.Models[harness.Codex]) > 0 {
		kinds[1], sources[1] = harness.Codex, KindFromBestOf
		if kinds[0] == harness.Codex {
			kinds[1] = harness.Claude
		}
	}

	var runs [2]*bestOfRun
	var branches [2]string
	for i, s := range slots {
		branches[i] = BranchName(s.Name(), id, it.Title)
	}
	for i := range runs {
		runs[i] = &bestOfRun{sibling: slots[1-i].Name(), siblingBranch: branches[1-i], kind: kinds[i], kindSource: sources[i], preflight: true}
	}
	var checked [2]Assigned
	for i := range runs {
		ao := o
		ao.Agent = slots[i].Name()
		if checked[i], err = e.assignOne(ctx, root, be, ao, it, runs[i]); err != nil {
			return checked[0], err
		}
	}
	if o.CheckOnly {
		first := checked[0]
		first.BestOf = []Assigned{checked[0], checked[1]}
		return first, nil
	}

	rnd := registryRound(root)
	var attempts []worker.BestOfAttempt
	for i, s := range slots {
		attempts = append(attempts, worker.BestOfAttempt{Slot: s.Name(), Branch: branches[i], ClaimID: s.Name() + "@" + strconv.Itoa(rnd)})
	}
	if err := worker.Update(root, func(d *worker.Doc) {
		b := worker.BestOf{Issue: id, Attempts: attempts, Round: rnd}
		if rec != nil {
			b.Pick = rec.Pick
		}
		d.SetBestOf(b)
	}); err != nil {
		return res, wrap(err)
	}
	var done [2]Assigned
	for i := range runs {
		runs[i].preflight = false
		ao := o
		ao.Agent = slots[i].Name()
		done[i], err = e.assignOne(ctx, root, be, ao, it, runs[i])
		if err == nil {
			continue
		}
		if i == 0 {
			// The first attempt undid itself: no record is left for a gate to refuse on.
			// Rollback path: the registry was read strictly above.
			if rec == nil && worker.LoadRegistryTolerant(root).Slot(slots[0].Name()).HeldID() != want {
				worker.ClearBestOf(root, id)
			}
			return done[0], err
		}
		first := done[0]
		first.Warnings = append(first.Warnings, fmt.Sprintf("the second best-of attempt (%s) failed: %v; the first runs alone, re-run assign to retry", slots[1].Name(), err))
		first.BestOf = []Assigned{done[0]}
		return first, err
	}
	first := done[0]
	first.BestOf = []Assigned{done[0], done[1]}
	return first, nil
}

func (b *bestOfRun) otherSlot() string {
	if b == nil {
		return ""
	}
	return b.sibling
}

func (b *bestOfRun) otherBranch() string {
	if b == nil {
		return ""
	}
	return b.siblingBranch
}
