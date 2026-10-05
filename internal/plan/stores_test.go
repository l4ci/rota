package plan

import (
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/artifact"
	"github.com/l4ci/rota/internal/artifact/artifacttest"
	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/exitcode"
)

// slicesFake is an in-memory SliceStore.
type slicesFake struct{ m map[[2]string]string }

func (f *slicesFake) SliceUnits(mid string) ([]string, error) {
	var out []string
	for k := range f.m {
		if k[0] == mid {
			out = append(out, k[1])
		}
	}
	sort.Strings(out)
	return out, nil
}

func (f *slicesFake) SliceGet(mid, unit string) (string, bool, error) {
	t, ok := f.m[[2]string{mid, unit}]
	return t, ok, nil
}

func (f *slicesFake) SlicePut(mid, unit, text string) (bool, error) {
	k := [2]string{mid, unit}
	if old, ok := f.m[k]; ok && old == text {
		return false, nil
	}
	f.m[k] = text
	return true, nil
}

func (f *slicesFake) SliceRm(mid, unit string) (bool, error) {
	k := [2]string{mid, unit}
	_, ok := f.m[k]
	delete(f.m, k)
	return ok, nil
}

func (f *slicesFake) SlicePlans(mid string) ([]backlog.SlicePlan, error) {
	units, _ := f.SliceUnits(mid)
	out := []backlog.SlicePlan{}
	for _, u := range units {
		out = append(out, backlog.SlicePlan{Milestone: mid, Unit: u, Text: f.m[[2]string{mid, u}]})
	}
	return out, nil
}

// Both stores answer the verbs the same way, for item plans and slice plans:
// same keys, exits, hints and changed flags.
func TestStoresShareTheVerbs(t *testing.T) {
	notes, slices := artifacttest.NewNotes(), &slicesFake{m: map[[2]string]string{}}
	var warned []string
	root := project(t)
	for _, s := range []struct {
		name string
		st   Store
	}{
		{"files", Files(root)},
		{"notes", NewNotes(
			func(string) (artifact.Notes, error) { return notes, nil },
			func() (SliceStore, error) { return slices, nil },
			func(m string) { warned = append(warned, m) })},
	} {
		t.Run(s.name, func(t *testing.T) {
			st := s.st
			if _, _, err := Add(root, st, AddOpts{Key: "M01-B07", Title: "Item"}); err != nil {
				t.Fatalf("add item plan: %v", err)
			}
			if _, _, err := Add(root, st, AddOpts{Key: "M01-B07", Title: "Item"}); exitOf(err) != 4 {
				t.Errorf("duplicate item plan: %v, want exit 4", err)
			}
			for i, title := range []string{"One", "Two"} {
				key, kind, err := Add(root, st, AddOpts{Milestone: "M01", Slice: true, Title: title})
				if want := "M01-S0" + strconv.Itoa(i+1); err != nil || key != want || kind != "slice" {
					t.Errorf("minted slice: %q %q %v, want %s", key, kind, err, want)
				}
			}
			if _, _, err := Add(root, st, AddOpts{Key: "M01-S02", Title: "x"}); exitOf(err) != 4 {
				t.Errorf("duplicate slice plan: %v, want exit 4", err)
			}
			list, err := List(st, "M01")
			if err != nil {
				t.Fatal(err)
			}
			slicePlans := 0
			for _, e := range list {
				if e.UnitKind == "slice" {
					slicePlans++
				}
			}
			if slicePlans != 2 {
				t.Errorf("list: %d slice plans, want 2 (%+v)", slicePlans, list)
			}
			for _, key := range []string{"M01-B07", "M01-S01"} {
				got, err := Show(st, key)
				if err != nil || !strings.Contains(got, "key: "+key+"\n") {
					t.Errorf("show %s: %q %v", key, got, err)
				}
				if changed, err := Put(st, key, "# new\n"); err != nil || !changed {
					t.Errorf("put %s: %v %v", key, changed, err)
				}
				if changed, err := Put(st, key, "# new\n"); err != nil || changed {
					t.Errorf("identical put %s: %v %v", key, changed, err)
				}
			}
			if _, err := Show(st, "nope"); exitOf(err) != 2 {
				t.Errorf("malformed key: %v, want exit 2", err)
			}
			for _, key := range []string{"M01-B07", "M01-S01", "M01-S02"} {
				if err := Rm(st, key); err != nil {
					t.Errorf("rm %s: %v", key, err)
				}
				if err := Rm(st, key); exitOf(err) != 3 {
					t.Errorf("second rm %s: %v, want exit 3", key, err)
				}
			}
			_, err = Put(st, "M01-B07", "x")
			var ae *exitcode.Error
			if exitOf(err) != 3 || !asErr(err, &ae) || ae.Hint != "rota plan add M01-B07 --title <text>" {
				t.Errorf("put without a plan: %v, want exit 3 with the add hint", err)
			}
		})
	}
	if len(warned) != 1 {
		t.Errorf("the note store lists slice plans only and says so once, got %v", warned)
	}
}

// The unit's digit rule is the one place the stores differ in add: files are
// minted as B07, issue numbers have any count.
func TestAddUnitDigitsByStore(t *testing.T) {
	root := project(t)
	notes := NewNotes(
		func(string) (artifact.Notes, error) { return artifacttest.NewNotes(), nil },
		func() (SliceStore, error) { return &slicesFake{m: map[[2]string]string{}}, nil }, nil)
	if _, _, err := Add(root, Files(root), AddOpts{Key: "M01-B7", Title: "t"}); exitOf(err) != 2 {
		t.Errorf("files B7: %v, want exit 2", err)
	}
	if key, _, err := Add(root, notes, AddOpts{Key: "M01-B7", Title: "t"}); err != nil || key != "M01-B7" {
		t.Errorf("notes B7: %q %v", key, err)
	}
}
