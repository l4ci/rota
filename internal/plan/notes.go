package plan

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/artifact"
	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/frontmatter"
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

var (
	sliceKeyRe = regexp.MustCompile(`^(M\d{2,})-(S\d+)$`)
	itemKeyRe  = regexp.MustCompile(`^M\d{2,}-([BFT]\d+)$`)
	// issueDesignRe is --design in issue mode: an issue number ("3") or the
	// lettered form with any digit count ("F3").
	issueDesignRe = regexp.MustCompile(`^[BFT]?\d+$`)
)

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

func (notes) Digits() int { return 1 }

func (notes) ItemOnly() bool { return true }

// DesignRef: an item plan points at the item's own design note, a slice plan
// at the note of the item it names.
func (notes) DesignRef(slice bool, design string) (string, error) {
	if !issueDesignRe.MatchString(design) {
		return "", exitcode.Errf(exitcode.ExitUsage, "--design must be an item ID like 3 or F3, got %q", design)
	}
	if slice {
		return "note:" + design + ":design", nil
	}
	return "note:design", nil
}

// sliceOf splits a slice plan key (M01-S03) into milestone and unit.
func sliceOf(key string) (milestone, unit string, isSlice bool) {
	if m := sliceKeyRe.FindStringSubmatch(key); m != nil {
		return m[1], m[2], true
	}
	return "", "", false
}

// itemOf is the item of an item plan key (M01-B07 gives "B07").
func itemOf(key string) string {
	if ItemOnlyKey(key) {
		return strings.ToUpper(strings.TrimPrefix(key, "#"))
	}
	if m := itemKeyRe.FindStringSubmatch(key); m != nil {
		return m[1]
	}
	return ""
}

func missing(what string) *exitcode.Error {
	return exitcode.Errf(exitcode.ExitResolution, "plan note for %s not found", what)
}

func (s notes) Create(milestone, unit string, render func(unit string) string) (string, error) {
	if unit != "" && !strings.HasPrefix(unit, "S") {
		n, err := s.item(unit)
		if err != nil {
			return "", err
		}
		if _, ok, err := n.NoteGet(unit, "plan"); err != nil {
			return "", err
		} else if ok {
			return "", exitcode.Errf(exitcode.ExitRefused, "plan note for %s already exists", unit)
		}
		_, err = n.NotePut(unit, "plan", render(unit))
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
		next := 0
		for _, u := range units {
			if n, err := strconv.Atoi(u[1:]); err == nil && n > next {
				next = n
			}
		}
		unit = fmt.Sprintf("S%02d", next+1)
	}
	key := milestone + "-" + unit
	if _, ok, err := sl.SliceGet(milestone, unit); err != nil {
		return "", err
	} else if ok {
		return "", exitcode.Errf(exitcode.ExitRefused, "plan note for %s already exists", key)
	}
	_, err = sl.SlicePut(milestone, unit, render(unit))
	return key, err
}

// Read adds the newline the old helper printed after a note.
func (s notes) Read(key string) (string, error) {
	var text string
	var ok bool
	var what string
	if m, u, isSlice := sliceOf(key); isSlice {
		sl, err := s.slices()
		if err != nil {
			return "", err
		}
		what = key
		if text, ok, err = sl.SliceGet(m, u); err != nil {
			return "", err
		}
	} else {
		item := itemOf(key)
		n, err := s.item(item)
		if err != nil {
			return "", err
		}
		what = item
		if text, ok, err = n.NoteGet(item, "plan"); err != nil {
			return "", err
		}
	}
	if !ok {
		return "", missing(what)
	}
	return text + "\n", nil
}

func (s notes) Replace(key, text string) (bool, error) {
	if m, u, isSlice := sliceOf(key); isSlice {
		sl, err := s.slices()
		if err != nil {
			return false, err
		}
		if _, ok, err := sl.SliceGet(m, u); err != nil {
			return false, err
		} else if !ok {
			return false, missing(key)
		}
		return sl.SlicePut(m, u, text)
	}
	item := itemOf(key)
	n, err := s.item(item)
	if err != nil {
		return false, err
	}
	if _, ok, err := n.NoteGet(item, "plan"); err != nil {
		return false, err
	} else if !ok {
		return false, missing(item)
	}
	return n.NotePut(item, "plan", text)
}

func (s notes) Remove(key string) error {
	var removed bool
	var what string
	if m, u, isSlice := sliceOf(key); isSlice {
		sl, err := s.slices()
		if err != nil {
			return err
		}
		what = key
		if removed, err = sl.SliceRm(m, u); err != nil {
			return err
		}
	} else {
		item := itemOf(key)
		n, err := s.item(item)
		if err != nil {
			return err
		}
		what = item
		if removed, err = n.NoteRm(item, "plan"); err != nil {
			return err
		}
	}
	if !removed {
		return missing(what)
	}
	return nil
}

// List is the slice plans of one milestone (or all with ""), their fields
// read from each note's frontmatter. Item plans are not listed: they live on
// their issues.
func (s notes) List(milestone string) ([]Entry, error) {
	sl, err := s.slices()
	if err != nil {
		return nil, err
	}
	if s.warn != nil {
		s.warn("item plans live on their issues (backlog.backend \"issues\"); listing slice plans from the milestone tracking issues only")
	}
	plans, err := sl.SlicePlans(milestone)
	if err != nil {
		return nil, err
	}
	out := []Entry{}
	for _, p := range plans {
		fm, _, _ := frontmatter.Parse(p.Text)
		e := entryOf(fm, p.Milestone+"-"+p.Unit, orDefault(frontmatter.Str(fm, "milestone"), p.Milestone), "", "slice")
		e.Unit = orDefault(frontmatter.Str(fm, "unit"), p.Unit)
		out = append(out, e)
	}
	return out, nil
}
