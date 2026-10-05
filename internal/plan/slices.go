package plan

import (
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"regexp"
	"strconv"

	"github.com/l4ci/rota/internal/artifact"
	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/frontmatter"
)

// Issue mode, slice plans: a `plan:S<NN>` note on the milestone's tracking
// issue. backlog.Issues implements SliceStore.

// SliceStore is the slice-plan half of backlog.Issues.
type SliceStore interface {
	SliceUnits(mid string) ([]string, error)
	SliceGet(mid, unit string) (string, bool, error)
	SlicePut(mid, unit, text string) (bool, error)
	SliceRm(mid, unit string) (bool, error)
	SlicePlans(mid string) ([]backlog.SlicePlan, error)
}

var sliceKeyRe = regexp.MustCompile(`^(M\d{2,})-(S\d+)$`)

// SliceOf splits a slice plan key (M01-S03) into milestone and unit.
func SliceOf(key string) (milestone, unit string, isSlice bool) {
	if m := sliceKeyRe.FindStringSubmatch(key); m != nil {
		return m[1], m[2], true
	}
	return "", "", false
}

func sliceMissing(key string) *exitcode.Error {
	return exitcode.Errf(exitcode.ExitResolution, "plan note for %s not found", key)
}

// AddSliceNote creates a slice plan note on the milestone's tracking issue.
// o names an explicit S-key or --milestone with --slice (the next S<NN> is
// minted from the existing notes). An existing note is exit 4; a milestone
// without a tracking issue is exit 3 (the store's ErrNotFound).
func AddSliceNote(root string, s SliceStore, o AddOpts) (key string, err error) {
	milestone, unit, err := parseAdd(o, true)
	if err != nil {
		return "", err
	}
	design, repo, err := extras(root, o, true)
	if err != nil {
		return "", err
	}
	if o.Design != "" {
		design = "note:" + o.Design + ":design"
	}
	if unit == "" {
		units, err := s.SliceUnits(milestone)
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
	key = milestone + "-" + unit
	if _, ok, err := s.SliceGet(milestone, unit); err != nil {
		return "", err
	} else if ok {
		return "", exitcode.Errf(exitcode.ExitRefused, "plan note for %s already exists", key)
	}
	_, err = s.SlicePut(milestone, unit, stub(key, milestone, unit, "slice", repo, design, o.Title))
	return key, err
}

// ShowSliceNote is the slice plan; the old helper printed it with a newline.
func ShowSliceNote(s SliceStore, key string) (string, error) {
	m, u, _ := SliceOf(key)
	text, ok, err := s.SliceGet(m, u)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", sliceMissing(key)
	}
	return text + "\n", nil
}

// PutSliceNote replaces an existing slice plan; changed is false when the
// note already reads as text.
func PutSliceNote(s SliceStore, key, text string) (bool, error) {
	m, u, _ := SliceOf(key)
	if _, ok, err := s.SliceGet(m, u); err != nil {
		return false, err
	} else if !ok {
		return false, sliceMissing(key).WithHint("rota plan add " + key + " --title <text>")
	}
	return s.SlicePut(m, u, text)
}

// RmSliceNote deletes the slice plan; a missing one is exit 3.
func RmSliceNote(s SliceStore, key string) error {
	m, u, _ := SliceOf(key)
	removed, err := s.SliceRm(m, u)
	if err != nil {
		return err
	}
	if !removed {
		return sliceMissing(key)
	}
	return nil
}

// ListSlices is the slice plans of one milestone (or all with ""), their
// fields read from each note's frontmatter. Item plans are not listed: they
// live on their issues.
func ListSlices(s SliceStore, milestone string) ([]Entry, error) {
	plans, err := s.SlicePlans(milestone)
	if err != nil {
		return nil, err
	}
	out := []Entry{}
	for _, p := range plans {
		fm, _, _ := frontmatter.Parse(p.Text)
		e := Entry{
			Key:       orDefault(frontmatter.Str(fm, "key"), p.Milestone+"-"+p.Unit),
			Milestone: orDefault(frontmatter.Str(fm, "milestone"), p.Milestone),
			Unit:      orDefault(frontmatter.Str(fm, "unit"), p.Unit),
			UnitKind:  orDefault(frontmatter.Str(fm, "unitKind"), "slice"),
			Title:     frontmatter.Str(fm, "title"),
			Status:    orDefault(frontmatter.Str(fm, "status"), "planned"),
			Created:   frontmatter.Str(fm, "created"),
			Repos:     []string{},
		}
		switch r := fm["repo"].(type) {
		case string:
			e.Repos = artifact.SplitCSV(r)
		case []string:
			e.Repos = append(e.Repos, r...)
		}
		out = append(out, e)
	}
	return out, nil
}
