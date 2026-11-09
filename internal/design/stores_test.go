package design

import (
	"errors"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/artifact"
	"github.com/l4ci/rota/internal/artifact/artifacttest"
	"github.com/l4ci/rota/internal/exitcode"
)

// stores are both Stores with an ID each accepts: the file store wants two
// digits, the note store any number.
func stores(t *testing.T) map[string]struct {
	st Store
	id string
} {
	notes := artifacttest.NewNotes()
	return map[string]struct {
		st Store
		id string
	}{
		"files": {Files(project(t)), "B07"},
		"notes": {NewNotes(func() (artifact.Notes, error) { return notes, nil }), "B7"},
	}
}

// Both stores answer the verbs the same way: same exits, same hints, same
// changed flags.
func TestStoresShareTheVerbs(t *testing.T) {
	for name, s := range stores(t) {
		t.Run(name, func(t *testing.T) {
			st, id := s.st, s.id
			if _, err := Show(st, id); exitOf(err) != 3 {
				t.Errorf("show before add: %v, want exit 3", err)
			}
			if err := Add(st, id, "Title", testDay); err != nil {
				t.Fatalf("add: %v", err)
			}
			if err := Add(st, id, "again", testDay); exitOf(err) != 4 {
				t.Errorf("duplicate add: %v, want exit 4", err)
			}
			got, err := Show(st, id)
			if err != nil || !strings.HasPrefix(got, "---\nid: "+id+"\ntitle: Title\nstatus: draft\n") {
				t.Errorf("show: %q %v", got, err)
			}
			if changed, err := Put(st, id, "# new\n"); err != nil || !changed {
				t.Errorf("put: %v %v", changed, err)
			}
			if changed, err := Put(st, id, "# new\n"); err != nil || changed {
				t.Errorf("identical put: %v %v", changed, err)
			}
			if err := Rm(st, id); err != nil {
				t.Errorf("rm: %v", err)
			}
			if err := Rm(st, id); exitOf(err) != 3 {
				t.Errorf("second rm: %v, want exit 3", err)
			}
			_, err = Put(st, id, "x")
			var ae *exitcode.Error
			if exitOf(err) != 3 || !errors.As(err, &ae) || ae.Hint != "rota design add "+id+" --title <text>" {
				t.Errorf("put without a design: %v, want exit 3 with the add hint", err)
			}
		})
	}
}

// The digit rule is the one place the stores differ for lettered IDs: file
// designs are minted as B07, so B7 is a typo there, while issue numbers have
// any digit count.
func TestFileIDRules(t *testing.T) {
	st := stores(t)
	for _, c := range []struct {
		store, id string
		exit      int
	}{
		{"files", "B7", 2}, {"files", "B07", 0}, {"files", "B1234", 0},
		{"notes", "B7", 0}, {"notes", "B07", 0},
		{"files", "S01", 2}, {"files", "7", 2}, {"files", "#7", 2}, {"files", "#B07", 2},
		{"files", "PROJ-123", 2},
	} {
		if exit := exitOf(st[c.store].st.CheckID(c.id)); exit != c.exit {
			t.Errorf("%s %q: exit %d, want %d", c.store, c.id, exit, c.exit)
		}
	}
}

// The note store reads item references through the backlog's grammar: numbers,
// lettered numbers and tracker keys all create, read, replace and remove the
// design of the one item they name, and the stub carries the normalized ID
// (never a "#", which would turn the front-matter id into a YAML comment).
func TestNotesStoreItemRefs(t *testing.T) {
	for _, c := range []struct{ id, item string }{
		{"42", "42"}, {"#42", "42"}, {"F7", "F7"}, {"PROJ-123", "PROJ-123"}, {"ENG-42", "ENG-42"}, {"proj-123", "PROJ-123"},
	} {
		t.Run(c.id, func(t *testing.T) {
			notes := artifacttest.NewNotes()
			st := NewNotes(func() (artifact.Notes, error) { return notes, nil })
			if err := Add(st, c.id, "T", testDay); err != nil {
				t.Fatalf("add: %v", err)
			}
			if _, ok := notes.M[[2]string{c.item, "design"}]; !ok {
				t.Fatalf("design not stored on item %s: %v", c.item, notes.M)
			}
			got, err := Show(st, c.item)
			if err != nil || !strings.HasPrefix(got, "---\nid: "+c.item+"\ntitle: T\n") {
				t.Errorf("show: %q %v", got, err)
			}
			if changed, err := Put(st, c.id, "# new\n"); err != nil || !changed {
				t.Errorf("put: %v %v", changed, err)
			}
			if err := Rm(st, c.id); err != nil {
				t.Errorf("rm: %v", err)
			}
			if len(notes.M) != 0 {
				t.Errorf("notes left behind: %v", notes.M)
			}
		})
	}
}

// A malformed reference is exit 2 from every verb before the store opens the
// item's notes, so the tracker is never reached.
func TestNotesStoreRejectsMalformedBeforeTracker(t *testing.T) {
	st := NewNotes(func() (artifact.Notes, error) {
		t.Fatal("tracker reached")
		return nil, nil
	})
	for _, id := range []string{"", "#", "S01", "#B7", "x/y", "233x", "PROJ-", "-123", "PROJ-12-3", " 42", "42 ", "PROJ 123", "M01-B07"} {
		if err := Add(st, id, "T", testDay); exitOf(err) != 2 {
			t.Errorf("add %q: %v, want exit 2", id, err)
		}
		if _, err := Show(st, id); exitOf(err) != 2 {
			t.Errorf("show %q: %v, want exit 2", id, err)
		}
		if _, err := Put(st, id, "x"); exitOf(err) != 2 {
			t.Errorf("put %q: %v, want exit 2", id, err)
		}
		if err := Rm(st, id); exitOf(err) != 2 {
			t.Errorf("rm %q: %v, want exit 2", id, err)
		}
	}
}

// A wrong ID names the shape the configured backend expects.
func TestCheckIDErrorNamesBackendForm(t *testing.T) {
	st := stores(t)
	if err := st["files"].st.CheckID("233x"); err == nil || !strings.Contains(err.Error(), `\d{2,}`) {
		t.Errorf("files error: %v", err)
	}
	if err := st["notes"].st.CheckID("233x"); err == nil || !strings.Contains(err.Error(), "#N") {
		t.Errorf("notes error: %v", err)
	}
}

// Amend works over either store: same section edit, same exits, same changed flag.
func TestStoresShareAmend(t *testing.T) {
	for name, s := range stores(t) {
		t.Run(name, func(t *testing.T) {
			st, id := s.st, s.id
			if _, err := Amend(st, id, "Goal", "append", "x"); exitOf(err) != 3 {
				t.Errorf("amend before add: %v, want exit 3", err)
			}
			if err := Add(st, id, "Title", testDay); err != nil {
				t.Fatal(err)
			}
			if changed, err := Amend(st, id, "Goal", "replace", "new goal"); err != nil || !changed {
				t.Fatalf("amend: changed=%v err=%v", changed, err)
			}
			if body, _ := Show(st, id); !strings.Contains(body, "new goal") {
				t.Errorf("amend not stored:\n%s", body)
			}
			if changed, _ := Amend(st, id, "Goal", "replace", "new goal\n\n"); changed {
				t.Error("identical amend reported changed")
			}
			if _, err := Amend(st, id, "No such", "append", "x"); exitOf(err) != 3 {
				t.Errorf("missing section: %v, want exit 3", err)
			}
			if _, err := Amend(st, id, "Goal", "prepend", "x"); exitOf(err) != 2 {
				t.Errorf("bad mode: %v, want exit 2", err)
			}
			if _, err := Amend(st, "S01", "Goal", "append", "x"); exitOf(err) != 2 {
				t.Errorf("bad id: %v, want exit 2", err)
			}
		})
	}
}
