package proof

import (
	"testing"

	"github.com/l4ci/rota/internal/artifact"
	"github.com/l4ci/rota/internal/artifact/artifacttest"
)

// Both stores keep rows the same way: same idempotence, same row format, no
// rows for an item that has none yet.
func TestStoresShareTheVerbs(t *testing.T) {
	notes := artifacttest.NewNotes()
	root := project(t)
	for _, s := range []struct {
		name, id string
		st       Store
	}{
		{"files", "B07", Files(root)},
		{"notes", "7", NewNotes(func() (artifact.Notes, error) { return notes, nil })},
	} {
		t.Run(s.name, func(t *testing.T) {
			if rows, lines, err := Show(s.st, s.id); err != nil || len(rows) != 0 || len(lines) != 0 {
				t.Fatalf("show before add: %v %v %v", rows, lines, err)
			}
			o := AddOpts{Check: "unit  tests", Result: "PASS", Evidence: "go test ok", Sha: "abc1234"}
			if row, changed, err := Add(s.st, root, s.id, o); err != nil || !changed || row.Check != "unit tests" {
				t.Fatalf("add: %+v %v %v", row, changed, err)
			}
			if _, changed, err := Add(s.st, root, s.id, o); err != nil || changed {
				t.Errorf("identical add: changed=%v err=%v", changed, err)
			}
			o.Result = "FAIL"
			if _, changed, err := Add(s.st, root, s.id, o); err != nil || !changed {
				t.Errorf("second row: changed=%v err=%v", changed, err)
			}
			rows, _, err := Show(s.st, s.id)
			if err != nil || len(rows) != 2 || rows[0].Result != "PASS" || rows[1].Result != "FAIL" {
				t.Errorf("show: %+v %v", rows, err)
			}
			if _, _, err := Add(s.st, root, s.id, AddOpts{Check: " ", Result: "PASS", Evidence: "e"}); exitOf(err) != 2 {
				t.Errorf("empty check: %v, want exit 2", err)
			}
			if _, _, err := Add(s.st, root, s.id, AddOpts{Check: "c", Result: "MAYBE", Evidence: "e"}); exitOf(err) != 2 {
				t.Errorf("bad result: %v, want exit 2", err)
			}
		})
	}
}
