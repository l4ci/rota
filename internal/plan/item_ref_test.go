package plan

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/artifact"
	"github.com/l4ci/rota/internal/artifact/artifacttest"
)

// itemStore is the note store over an in-memory Notes. items records each
// item the store opens, so a test sees which item a key resolved to.
func itemStore(t *testing.T) (Store, *artifacttest.Notes, *[]string) {
	t.Helper()
	notes, opened, slices := artifacttest.NewNotes(), &[]string{}, &slicesFake{m: map[[2]string]string{}}
	st := NewNotes(
		func(item string) (artifact.Notes, error) { *opened = append(*opened, item); return notes, nil },
		func() (SliceStore, error) { return slices, nil }, nil)
	return st, notes, opened
}

// The note store takes the item references the issue backend takes, bare or
// under a milestone, and resolves each to the one item it names. A tracker key
// keeps its hyphen and is never read as a slice, even when it starts with S.
func TestNotesStoreItemRefs(t *testing.T) {
	root := project(t)
	for _, c := range []struct{ key, item string }{
		{"42", "42"}, {"#42", "42"}, {"B7", "B7"}, {"PROJ-123", "PROJ-123"}, {"ENG-42", "ENG-42"},
		{"STORY-12", "STORY-12"}, {"proj-123", "PROJ-123"}, {"M01-B07", "B07"}, {"M01-B7", "B7"},
	} {
		t.Run(c.key, func(t *testing.T) {
			st, notes, opened := itemStore(t)
			key, kind, err := Add(root, st, AddOpts{Key: c.key, Title: "Item"}, testDay)
			if err != nil || key != c.key || kind != "item" {
				t.Fatalf("add: %q %q %v", key, kind, err)
			}
			if _, ok := notes.M[[2]string{c.item, "plan"}]; !ok {
				t.Fatalf("plan not stored on item %s: %v", c.item, notes.M)
			}
			if _, _, err := Add(root, st, AddOpts{Key: c.key, Title: "Item"}, testDay); exitOf(err) != 4 {
				t.Errorf("duplicate: %v, want exit 4", err)
			}
			got, err := Show(st, c.key)
			if err != nil || !strings.Contains(got, "unitKind: item\n") {
				t.Errorf("show: %q %v", got, err)
			}
			if changed, err := Put(st, c.key, "# new\n"); err != nil || !changed {
				t.Errorf("put: %v %v", changed, err)
			}
			if err := Rm(st, c.key); err != nil {
				t.Errorf("rm: %v", err)
			}
			if len(notes.M) != 0 {
				t.Errorf("notes left behind: %v", notes.M)
			}
			for _, o := range *opened {
				if o != c.item {
					t.Errorf("opened item %q, want only %q", o, c.item)
				}
			}
		})
	}
}

// A malformed key is exit 2 from every verb before the store opens a note, an
// item or a slice, so the tracker is never reached.
func TestNotesStoreRejectsMalformedBeforeTracker(t *testing.T) {
	root := project(t)
	st := NewNotes(
		func(string) (artifact.Notes, error) { t.Fatal("item opened"); return nil, nil },
		func() (SliceStore, error) { t.Fatal("slices opened"); return nil, nil }, nil)
	for _, key := range []string{"", "#", "x/y", "233x", "PROJ-", "-123", "PROJ-12-3", " 42", "42 ", "PROJ 123", "#B7", "M01-", "M01-S", "M1-B07"} {
		if _, _, err := Add(root, st, AddOpts{Key: key, Title: "T"}, testDay); exitOf(err) != 2 {
			t.Errorf("add %q: %v, want exit 2", key, err)
		}
		if _, err := Show(st, key); exitOf(err) != 2 {
			t.Errorf("show %q: %v, want exit 2", key, err)
		}
		if _, err := Put(st, key, "x"); exitOf(err) != 2 {
			t.Errorf("put %q: %v, want exit 2", key, err)
		}
		if err := Rm(st, key); exitOf(err) != 2 {
			t.Errorf("rm %q: %v, want exit 2", key, err)
		}
	}
}

// --design in issue mode is read by the same grammar, and a slice plan's
// pointer names the item's design note with the normalized ID.
func TestNotesDesignRefTakesItemRefs(t *testing.T) {
	root := project(t)
	for _, c := range []struct{ design, ref string }{
		{"3", "3"}, {"F3", "F3"}, {"#3", "3"}, {"PROJ-123", "PROJ-123"},
	} {
		st, _, _ := itemStore(t)
		if _, _, err := Add(root, st, AddOpts{Milestone: "M01", Slice: true, Title: "S", Design: c.design}, testDay); err != nil {
			t.Fatalf("slice %s: %v", c.design, err)
		}
		got, err := Show(st, "M01-S01")
		if err != nil || !strings.Contains(got, "design: note:"+c.ref+":design\n") {
			t.Errorf("slice design %s: %q %v", c.design, got, err)
		}
		if _, _, err := Add(root, st, AddOpts{Key: c.ref, Title: "I", Design: c.design}, testDay); err != nil {
			t.Fatalf("item %s: %v", c.design, err)
		}
		got, err = Show(st, c.ref)
		if err != nil || !strings.Contains(got, "design: note:design\n") {
			t.Errorf("item design %s: %q %v", c.design, got, err)
		}
	}
	st, _, _ := itemStore(t)
	for _, bad := range []string{"S01", "x", "PROJ-", "#B3", "M01-B07"} {
		if _, _, err := Add(root, st, AddOpts{Milestone: "M01", Slice: true, Title: "S", Design: bad}, testDay); exitOf(err) != 2 {
			t.Errorf("--design %q: %v, want exit 2", bad, err)
		}
	}
}

// Resolve splits a key once: a slice, an item under a milestone, or a bare
// item reference. Milestone/slice keys keep their own grammar.
func TestResolve(t *testing.T) {
	for _, c := range []struct {
		key, milestone, unit string
		slice, ok            bool
	}{
		{"M01-S02", "M01", "S02", true, true},
		{"M12-B07", "M12", "B07", false, true},
		{"#7", "", "7", false, true},
		{"PROJ-123", "", "PROJ-123", false, true},
		{"STORY-1", "", "STORY-1", false, true},
		{"S01", "", "", false, false},
		{"M01", "", "", false, false},
		{"M01-X07", "", "", false, false},
	} {
		r, ok := Resolve(c.key)
		if ok != c.ok || (ok && (r.Milestone != c.milestone || r.UnitID() != c.unit || r.Slice != c.slice)) {
			t.Errorf("Resolve(%q) = %+v %v, want milestone %q unit %q slice %v ok %v", c.key, r, ok, c.milestone, c.unit, c.slice, c.ok)
		}
	}
}

// File plans keep their grammar: always under a milestone, minted zero-padded,
// and never a tracker key.
func TestFilesStoreKeepsFileKeys(t *testing.T) {
	root := project(t)
	st := Files(root)
	for _, key := range []string{"42", "#42", "B07", "PROJ-123", "M01-PROJ-123"} {
		if _, _, err := Add(root, st, AddOpts{Key: key, Title: "T"}, testDay); exitOf(err) != 2 {
			t.Errorf("add %q: %v, want exit 2", key, err)
		}
		if _, err := Show(st, key); exitOf(err) != 2 {
			t.Errorf("show %q: %v, want exit 2", key, err)
		}
	}
	for _, key := range []string{"M01-B7", "M01-S2"} {
		if _, _, err := Add(root, st, AddOpts{Key: key, Title: "T"}, testDay); exitOf(err) != 2 {
			t.Errorf("add %q: %v, want exit 2", key, err)
		}
	}
	if key, kind, err := Add(root, st, AddOpts{Key: "M01-S02", Title: "T"}, testDay); err != nil || key != "M01-S02" || kind != "slice" {
		t.Errorf("add M01-S02: %q %q %v", key, kind, err)
	}
}
