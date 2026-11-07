package backlog

import (
	"time"

	ms "github.com/l4ci/rota/internal/milestone"
)

// milestoneOps is the milestone method set Issues and Umbrella both carry: one
// repo, or an umbrella that mints IDs and aggregates status over every
// sub-repo's native milestones.
type milestoneOps interface {
	MilestoneAdd(mid, title, summary string, depends []string, kind, today string) (string, error)
	MilestoneList() ([]ms.Entry, error)
	MilestoneShow(mid string) (string, error)
	// MilestonePut and MilestoneStatus report whether the write changed the
	// milestone, so the store never reads it back to find out.
	MilestonePut(mid, text string) (changed bool, err error)
	MilestoneStatus(mid, status string) (changed bool, err error)
}

// MilestoneStore is the ms.Store of one repo's tracker.
func (b *Issues) MilestoneStore() ms.Store { return trackerMilestones{b, b.Today} }

// MilestoneStore is the ms.Store of an umbrella: the plan lives on the
// home sub-repo, IDs are minted and status aggregated over every sub-repo.
func (u *Umbrella) MilestoneStore() ms.Store { return trackerMilestones{u, u.Today} }

// trackerMilestones adapts milestoneOps to ms.Store.
type trackerMilestones struct {
	ops   milestoneOps
	today func() time.Time // the created date's clock; nil is the wall clock
}

func (t trackerMilestones) Add(title, summary string, depends []string, kind string) (string, error) {
	return t.ops.MilestoneAdd("", title, summary, depends, kind, ms.Day(t.today))
}

func (t trackerMilestones) List() ([]ms.Entry, error) { return t.ops.MilestoneList() }

// Show is the plan text plus the newline the old helper printed after it.
func (t trackerMilestones) Show(id string) (string, error) {
	text, err := t.ops.MilestoneShow(id)
	return text + "\n", err
}

func (t trackerMilestones) Put(id, text string) (bool, error) { return t.ops.MilestonePut(id, text) }

func (t trackerMilestones) SetStatus(id, status string) (bool, error) {
	return t.ops.MilestoneStatus(id, status)
}

func (trackerMilestones) OnTracker() bool { return true }
