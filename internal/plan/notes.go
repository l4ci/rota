package plan

import (
	"github.com/l4ci/rota/internal/artifact"
	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/frontmatter"
	"github.com/l4ci/rota/internal/strutil"
)

// SliceStore is the slice-plan half of backlog.Issues: a `plan:S<NN>` note on
// the milestone's tracking issue.
type SliceStore interface {
	SliceUnits(mid string) ([]string, error)
	SliceGet(mid, unit string) (string, bool, error)
	SlicePut(mid, unit, text string) (bool, error)
	SliceRm(mid, unit string) (bool, error)
	SlicePlans(mid string) ([]backlog.SlicePlan, error)
}

// notes keeps item plans as the `plan` note of the item's issue (the
// milestone part of the key is ignored) and slice plans on the milestone's
// tracking issue.
type notes struct {
	item   func(item string) (artifact.Notes, error)
	slices func() (SliceStore, error)
	warn   func(string)
}

// NewNotes is the issue-mode store. item resolves an item's notes (an unknown
// item is exit 3) and slices opens the slice plans, each on first use; warn
// receives notices, for example that List covers slice plans only.
func NewNotes(item func(string) (artifact.Notes, error), slices func() (SliceStore, error), warn func(string)) Store {
	return notes{item, slices, warn}
}

// Resolve: on the issue backend an item plan goes without its milestone.
func (notes) Resolve(key string) (Ref, error) {
	if r, ok := Resolve(key); ok {
		return r, nil
	}
	return Ref{}, exitcode.Errf(exitcode.ExitUsage, "key must look like #7, B7, PROJ-123, M01-B07 or M01-S02, got %q", key)
}

// CheckNew: issue numbers have any digit count, so there is nothing more.
func (notes) CheckNew(Ref) error { return nil }

// DesignRef: an item plan points at the item's own design note, a slice plan
// at the note of the item it names.
func (notes) DesignRef(slice bool, design string) (string, error) {
	ref, err := backlog.ParseIssueRef(design)
	if err != nil {
		return "", exitcode.Errf(exitcode.ExitUsage, "--design must be an item ID like 3, F3 or PROJ-123, got %q", design)
	}
	if slice {
		return "note:" + ref.ID + ":design", nil
	}
	return "note:design", nil
}

// docs opens a key's note: a slice plan is a slice of its milestone's
// tracking issue, an item plan the `plan` note of the item's issue.
func (s notes) docs() artifact.NoteDocs {
	return artifact.NoteDocs{Kind: "plan", Open: func(key string) (artifact.NoteSlot, string, error) {
		r, ok := Resolve(key)
		if !ok {
			return nil, "", exitcode.Errf(exitcode.ExitUsage, "key must look like #7, B7, PROJ-123, M01-B07 or M01-S02, got %q", key)
		}
		if r.Slice {
			sl, err := s.slices()
			if err != nil {
				return nil, "", err
			}
			return sliceNote{sl, r.Milestone, r.Unit}, key, nil
		}
		item := r.Item.ID
		n, err := s.item(item)
		if err != nil {
			return nil, "", err
		}
		return artifact.ItemNote{Notes: n, Ref: item, Kind: "plan"}, item, nil
	}}
}

// sliceNote is the NoteSlot of one slice plan.
type sliceNote struct {
	sl        SliceStore
	mid, unit string
}

func (n sliceNote) Get() (string, bool, error)    { return n.sl.SliceGet(n.mid, n.unit) }
func (n sliceNote) Put(text string) (bool, error) { return n.sl.SlicePut(n.mid, n.unit, text) }
func (n sliceNote) Rm() (bool, error)             { return n.sl.SliceRm(n.mid, n.unit) }

func (s notes) Create(r Ref, render func(unit string) string) (string, error) {
	milestone, unit := r.Milestone, r.UnitID()
	if !r.Slice {
		n, err := s.item(unit)
		if err != nil {
			return "", err
		}
		err = artifact.CreateNote("plan", artifact.ItemNote{Notes: n, Ref: unit, Kind: "plan"}, unit, render(unit))
		return milestone + "-" + unit, err
	}
	sl, err := s.slices()
	if err != nil {
		return "", err
	}
	if unit == "" {
		units, err := sl.SliceUnits(milestone)
		if err != nil {
			return "", err
		}
		unit = nextUnit(units)
	}
	key := milestone + "-" + unit
	err = artifact.CreateNote("plan", sliceNote{sl, milestone, unit}, key, render(unit))
	return key, err
}

func (s notes) Read(key string) (string, error) { return s.docs().Read(key) }

func (s notes) Replace(key, text string) (bool, error) { return s.docs().Replace(key, text) }

func (s notes) Remove(key string) error { return s.docs().Remove(key) }

// List is the slice plans of one milestone (or all with ""), their fields
// read from each note's frontmatter. Item plans are not listed: they live on
// their issues.
func (s notes) List(milestone string) ([]Entry, error) {
	sl, err := s.slices()
	if err != nil {
		return nil, err
	}
	if s.warn != nil {
		s.warn("item plans live on their issues (issues.source is a tracker); listing slice plans from the milestone tracking issues only")
	}
	plans, err := sl.SlicePlans(milestone)
	if err != nil {
		return nil, err
	}
	out := []Entry{}
	for _, p := range plans {
		fm, _, _ := frontmatter.Parse(p.Text)
		e := entryOf(fm, p.Milestone+"-"+p.Unit, strutil.OrDefault(frontmatter.Str(fm, "milestone"), p.Milestone), "", "slice")
		e.Unit = strutil.OrDefault(frontmatter.Str(fm, "unit"), p.Unit)
		out = append(out, e)
	}
	return out, nil
}
