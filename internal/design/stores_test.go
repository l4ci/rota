package design

import (
	"errors"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/artifact"
	"github.com/l4ci/rota/internal/artifact/artifacttest"
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
			if err := Add(st, id, "Title"); err != nil {
				t.Fatalf("add: %v", err)
			}
			if err := Add(st, id, "again"); exitOf(err) != 4 {
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
			var ae *artifact.Error
			if exitOf(err) != 3 || !errors.As(err, &ae) || ae.Hint != "rota design add "+id+" --title <text>" {
				t.Errorf("put without a design: %v, want exit 3 with the add hint", err)
			}
		})
	}
}

// The digit rule is the one place the stores differ: file designs are minted
// as B07, so B7 is a typo there, while issue numbers have any digit count.
func TestIDDigitsByStore(t *testing.T) {
	st := stores(t)
	for _, c := range []struct {
		store, id string
		exit      int
	}{
		{"files", "B7", 2}, {"files", "B07", 0}, {"files", "B1234", 0},
		{"notes", "B7", 0}, {"notes", "B07", 0},
		{"files", "S01", 2}, {"notes", "S01", 2}, {"files", "7", 2}, {"notes", "7", 2}, {"notes", "../x", 2},
	} {
		if exit := exitOf(CheckID(st[c.store].st, c.id)); exit != c.exit {
			t.Errorf("%s %q: exit %d, want %d", c.store, c.id, exit, c.exit)
		}
	}
}
