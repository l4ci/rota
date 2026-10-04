package backlog

import (
	"time"

	ms "github.com/l4ci/rota/internal/milestone"
)

// milestoneOps is the milestone method set Issues and Umbrella both carry: one
// repo, or an umbrella that mints IDs and aggregates status over every
// sub-repo's native milestones.
type milestoneOps interface {
	MilestoneAdd(mid, title, summary string, depends []string, today string) (string, error)
	MilestoneList() ([]ms.Entry, error)
	MilestoneShow(mid string) (string, error)
	MilestonePut(mid, text string) error
	MilestoneStatus(mid, status string) error
}

// MilestoneStore is the ms.Store of one repo's tracker.
func (b *Issues) MilestoneStore() ms.Store { return trackerMilestones{b} }

// MilestoneStore is the ms.Store of an umbrella: the plan lives on the
// home sub-repo, IDs are minted and status aggregated over every sub-repo.
func (u *Umbrella) MilestoneStore() ms.Store { return trackerMilestones{u} }

// trackerMilestones adapts milestoneOps to ms.Store.
type trackerMilestones struct{ ops milestoneOps }

func (t trackerMilestones) Add(title, summary string, depends []string) (string, error) {
	return t.ops.MilestoneAdd("", title, summary, depends, time.Now().Format("2006-01-02"))
}

func (t trackerMilestones) List() ([]ms.Entry, error) { return t.ops.MilestoneList() }

// Show is the plan text plus the newline the old helper printed after it.
func (t trackerMilestones) Show(id string) (string, error) {
	text, err := t.ops.MilestoneShow(id)
	return text + "\n", err
}

func (t trackerMilestones) Put(id, text string) (bool, error) {
	before, err := t.ops.MilestoneShow(id)
	if err != nil {
		return false, err
	}
	if err := t.ops.MilestonePut(id, text); err != nil {
		return false, err
	}
	after, err := t.ops.MilestoneShow(id)
	if err != nil {
		return false, err
	}
	return before != after, nil
}

func (t trackerMilestones) SetStatus(id, status string) (bool, error) {
	rows, err := t.ops.MilestoneList()
	if err != nil {
		return false, err
	}
	was := ""
	for _, r := range rows {
		if r.ID == id {
			was = r.Status
		}
	}
	if err := t.ops.MilestoneStatus(id, status); err != nil {
		return false, err
	}
	return was != status, nil
}

func (trackerMilestones) OnTracker() bool { return true }
