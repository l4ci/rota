package backlog

import (
	"errors"
	"fmt"
	"strconv"

	ms "github.com/l4ci/rota/internal/milestone"
)

// DecisionRef spells an item in the decision list: #12 for an issue number,
// the ID as it is for a file item (B07) or a tracker key (PROJ-12).
func DecisionRef(id string) string {
	if _, err := strconv.Atoi(id); err == nil {
		return "#" + id
	}
	return id
}

// MilestoneStore is the file backend's milestone store: detail files under
// .rota/milestones.
func (f *File) MilestoneStore() ms.Store {
	return ms.FileStore{Root: f.Root, Today: f.Today, ClosedItems: func(id string) ([]string, error) {
		items, err := f.milestoneItems(id)
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		var closed []string
		for _, it := range items {
			if it.Closed != "" {
				closed = append(closed, fmt.Sprintf("- %s: %s (%s)", it.ID, it.Title, it.Closed))
			}
		}
		return closed, nil
	}}
}

// LogDecision appends `- <ref>: <gist>` to the "## Decisions" list of the
// item's milestone (the first it names) and returns that milestone's ID. It is
// idempotent: a line for ref already there is left alone. An item with no
// milestone, or a backend with no milestone store, logs nothing and returns "".
func LogDecision(be Backend, it *Item, ref, gist string) (string, error) {
	s, ok := be.(interface{ MilestoneStore() ms.Store })
	if !ok {
		return "", nil
	}
	mids := ParseMilestones(it.Fields.Get("milestone"))
	if len(mids) == 0 {
		return "", nil
	}
	st := s.MilestoneStore()
	text, err := st.Show(mids[0])
	if err != nil {
		return "", err
	}
	if next, changed := ms.AppendDecision(text, ref, gist); changed {
		if _, err := st.Put(mids[0], next); err != nil {
			return "", err
		}
	}
	return mids[0], nil
}
