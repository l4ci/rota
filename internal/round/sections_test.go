package round

import (
	"fmt"
	"sync"
	"testing"

	"github.com/l4ci/rota/internal/escalation"
	"github.com/l4ci/rota/internal/limits"
	"github.com/l4ci/rota/internal/worker"
)

// The three round-level sections share one file and one lock: concurrent
// updates to different sections must lose no writes.
func TestSectionsConcurrentUpdatesLoseNothing(t *testing.T) {
	root := t.TempDir()
	const n = 15
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			if err := escalation.Update(root, func(l []escalation.Entry) []escalation.Entry {
				return append(l, escalation.Entry{ID: fmt.Sprintf("e%d", len(l)+1), Kind: "issue", Status: escalation.StatusPending})
			}); err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := limits.Append(root, limits.Entry{Status: limits.StatusWaiting}); err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := worker.Update(root, func(d *worker.Doc) { RecordReviewItems(d, []string{"x"}) }); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	reg := worker.LoadRegistryTolerant(root)
	if got := len(escalation.FromRegistry(reg)); got != n {
		t.Errorf("escalations = %d, want %d", got, n)
	}
	if got := len(limits.FromRegistry(reg)); got != n {
		t.Errorf("limits = %d, want %d", got, n)
	}
	if got := len(ReviewOf(reg).Items); got != n {
		t.Errorf("review items = %d, want %d", got, n)
	}
}
