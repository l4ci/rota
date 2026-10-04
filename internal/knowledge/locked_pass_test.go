package knowledge

import (
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
)

// Contract note (hit, tier set, contradiction add): the port reads the value it
// reports in the same locked pass as the write, so concurrent writers never see
// each other's result. If a value were read after the lock was released, two
// callers could report the same count, or two could claim the one change.

func lockedStore(t *testing.T) Store {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	return Store{Root: root}
}

func parallel(n int, f func(i int)) {
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); f(i) }(i)
	}
	wg.Wait()
}

func TestHitReportsItsOwnCountUnderConcurrency(t *testing.T) {
	s := lockedStore(t)
	const n = 12
	hits := make([]int, n)
	parallel(n, func(i int) {
		r, err := s.Hit(Umbrella, "Architecture: A", "bullet")
		if err != nil {
			t.Error(err)
			return
		}
		hits[i] = r.Entry.Hits
	})
	sort.Ints(hits)
	for i, h := range hits {
		if h != i+1 {
			t.Fatalf("reported hits %v, want 1..%d each exactly once", hits, n)
		}
	}
}

func TestTierSetReportsExactlyOneChangeUnderConcurrency(t *testing.T) {
	s := lockedStore(t)
	const n = 12
	changed := 0
	var mu sync.Mutex
	parallel(n, func(int) {
		_, ch, err := s.TierSet(Umbrella, "Architecture: A", "bullet", Confirmed)
		if err != nil {
			t.Error(err)
			return
		}
		if ch {
			mu.Lock()
			changed++
			mu.Unlock()
		}
	})
	if changed != 1 {
		t.Fatalf("%d callers reported changed=true, want exactly 1", changed)
	}
}

func TestContradictionAddReportsItsOwnQueueLengthUnderConcurrency(t *testing.T) {
	s := lockedStore(t)
	const n = 12
	lens := make([]int, n)
	parallel(n, func(i int) {
		l, err := s.AddContradiction("Architecture: A", "bullet", "text")
		if err != nil {
			t.Error(err)
			return
		}
		lens[i] = l
	})
	sort.Ints(lens)
	for i, l := range lens {
		if l != i+1 {
			t.Fatalf("reported queue lengths %v, want 1..%d each exactly once", lens, n)
		}
	}
}
