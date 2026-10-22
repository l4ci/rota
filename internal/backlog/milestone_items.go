package backlog

import (
	"errors"
	"strings"

	ms "github.com/l4ci/rota/internal/milestone"
)

// MilestoneItems is the open items tagged with mid, in backlog order, as the
// milestone's generated Items section lists them.
func MilestoneItems(rows []Row, mid string) []ms.ItemRef {
	byID := map[string]Row{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	refs := []ms.ItemRef{}
	for _, id := range IDsByMilestone(rows, mid) {
		r := byID[id]
		refs = append(refs, ms.ItemRef{ID: id, Title: r.Title, Detail: strings.Trim(r.Fields.Detail, "` \t")})
	}
	return refs
}

// SyncMilestones regenerates the Items section of every milestone file from the
// open bullets, so the Milestone field and the list agree after a write. It
// returns the milestones it rewrote. A project without BACKLOG.md has no items
// to list.
func (f *File) SyncMilestones() ([]string, error) {
	rows, err := f.Rows()
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	list, err := ms.List(f.Root)
	if err != nil {
		return nil, err
	}
	var changed []string
	for _, m := range list {
		ok, err := ms.SyncItems(f.Root, m.ID, MilestoneItems(rows, m.ID))
		if err != nil {
			return changed, err
		}
		if ok {
			changed = append(changed, m.ID)
		}
	}
	return changed, nil
}

// syncMilestonesIf is SyncMilestones for a verb whose bullet carried a Milestone field.
func (f *File) syncMilestonesIf(tagged bool) error {
	if !tagged {
		return nil
	}
	_, err := f.SyncMilestones()
	return err
}

func bulletTagged(bullet string) bool { return ParseFields(bullet).Milestone != "" }
