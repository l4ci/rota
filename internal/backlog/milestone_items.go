package backlog

import (
	"errors"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	ms "github.com/l4ci/rota/internal/milestone"
	"github.com/l4ci/rota/internal/pystr"
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

// closedMilestoneItems is the completed items tagged with mid, from the done
// lines of text (## Completed, then ARCHIVE.md), each marked with its closure
// reason. Ids in seen are skipped, and added to it.
func closedMilestoneItems(text, mid string, seen map[string]bool) []ms.ItemRef {
	var refs []ms.ItemRef
	for _, raw := range pystr.Splitlines(text) {
		d, ok := ParseDone(pystr.Strip(raw))
		if !ok || d.ID == "" || seen[d.ID] {
			continue
		}
		fields := ParseFields(d.Inner)
		for _, m := range ParseMilestones(fields.Get("milestone")) {
			if m != mid {
				continue
			}
			seen[d.ID] = true
			title := d.ID
			if b, ok := ParseOpen("- " + d.Inner); ok {
				title = b.Title
			}
			refs = append(refs, ms.ItemRef{ID: d.ID, Title: title, Detail: strings.Trim(fields.Detail, "` \t"), Closed: d.Reason})
			break
		}
	}
	return refs
}

// SyncMilestones regenerates the Items section of every milestone file from the
// open bullets and the done lines (completed items stay listed, marked), so the Milestone field and the list agree after a write. It
// returns the milestones it rewrote. One milestone that fails to sync does not
// stop the rest; the errors come back joined. A project without BACKLOG.md has no items
// to list.
func (f *File) SyncMilestones() ([]string, error) {
	rows, err := f.Rows()
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	md, _ := f.Markdown(0)
	archive, _ := fsio.ReadText(f.rota("ARCHIVE.md"))
	list, err := ms.List(f.Root)
	if err != nil {
		return nil, err
	}
	var changed []string
	var errs []error
	for _, m := range list {
		items := MilestoneItems(rows, m.ID)
		seen := map[string]bool{}
		items = append(items, closedMilestoneItems(sectionBody(md, "Completed"), m.ID, seen)...)
		items = append(items, closedMilestoneItems(archive, m.ID, seen)...)
		ok, err := ms.SyncItems(f.Root, m.ID, items)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if ok {
			changed = append(changed, m.ID)
		}
	}
	return changed, errors.Join(errs...)
}

// syncMilestonesIf is SyncMilestones for a verb whose bullet carried a Milestone
// field. The write has already committed, so a failure is a warning, never the
// verb's error: a caller that retried a "failed" create would duplicate the item.
func (f *File) syncMilestonesIf(tagged bool) {
	if !tagged {
		return
	}
	if _, err := f.SyncMilestones(); err != nil && f.Warn != nil {
		f.Warn("milestone sync after the write: " + err.Error())
	}
}

func bulletTagged(bullet string) bool { return ParseFields(bullet).Milestone != "" }
