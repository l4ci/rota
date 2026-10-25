package proof

import (
	"fmt"
	"sync"
	"time"

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
		{"notes", "7", newTestNotes(root, notes)},
	} {
		t.Run(s.name, func(t *testing.T) {
			if rows, lines, err := Show(s.st, s.id); err != nil || len(rows) != 0 || len(lines) != 0 {
				t.Fatalf("show before add: %v %v %v", rows, lines, err)
			}
			o := AddOpts{Check: "unit  tests", Result: "PASS", Evidence: "go test ok", Sha: "abc1234"}
			if row, changed, err := Add(s.st, root, s.id, proofDay, o); err != nil || !changed || row.Check != "unit tests" {
				t.Fatalf("add: %+v %v %v", row, changed, err)
			}
			if _, changed, err := Add(s.st, root, s.id, proofDay, o); err != nil || changed {
				t.Errorf("identical add: changed=%v err=%v", changed, err)
			}
			o.Result = "FAIL"
			if _, changed, err := Add(s.st, root, s.id, proofDay, o); err != nil || !changed {
				t.Errorf("second row: changed=%v err=%v", changed, err)
			}
			rows, _, err := Show(s.st, s.id)
			if err != nil || len(rows) != 2 || rows[0].Result != "PASS" || rows[1].Result != "FAIL" {
				t.Errorf("show: %+v %v", rows, err)
			}
			if _, _, err := Add(s.st, root, s.id, proofDay, AddOpts{Check: " ", Result: "PASS", Evidence: "e"}); exitOf(err) != 2 {
				t.Errorf("empty check: %v, want exit 2", err)
			}
			if _, _, err := Add(s.st, root, s.id, proofDay, AddOpts{Check: "c", Result: "MAYBE", Evidence: "e"}); exitOf(err) != 2 {
				t.Errorf("bad result: %v, want exit 2", err)
			}
		})
	}
}

// slowNotes widens the read-modify-write window so overlapping appends collide.
type slowNotes struct {
	mu sync.Mutex
	*artifacttest.Notes
}

func (n *slowNotes) NoteGet(ref, kind string) (string, bool, error) {
	n.mu.Lock()
	t, ok, err := n.Notes.NoteGet(ref, kind)
	n.mu.Unlock()
	time.Sleep(20 * time.Millisecond)
	return t, ok, err
}

func (n *slowNotes) NotePut(ref, kind, text string) (bool, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.Notes.NotePut(ref, kind, text)
}

// Overlapping appends on one item keep every measured row, in both stores.
func TestConcurrentAddsKeepEveryRow(t *testing.T) {
	root := project(t)
	notes := &slowNotes{Notes: artifacttest.NewNotes()}
	for _, s := range []struct {
		name, id string
		st       Store
	}{
		{"files", "B07", Files(root)},
		{"notes", "7", newTestNotes(root, notes)},
	} {
		t.Run(s.name, func(t *testing.T) {
			const n = 4
			var wg sync.WaitGroup
			for i := 0; i < n; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					o := AddOpts{Check: fmt.Sprintf("check %d", i), Result: "PASS", Evidence: "ok", Sha: "abc1234"}
					if _, _, err := Add(s.st, root, s.id, proofDay, o); err != nil {
						t.Errorf("add %d: %v", i, err)
					}
				}(i)
			}
			wg.Wait()
			rows, _, err := Show(s.st, s.id)
			if err != nil || len(rows) != n {
				t.Errorf("rows = %d, want %d (err %v): %+v", len(rows), n, err, rows)
			}
		})
	}
}

func newTestNotes(root string, n artifact.Notes) Store {
	return NewNotes(root, func() (artifact.Notes, error) { return n, nil })
}
