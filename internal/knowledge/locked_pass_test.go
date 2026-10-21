package knowledge

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/fsio"
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

func TestRenameTopicMovesEveryBulletUnderConcurrency(t *testing.T) {
	s := lockedStore(t)
	const n = 16
	km, _ := s.KnowledgePath(Umbrella)
	var sb strings.Builder
	sb.WriteString("# Knowledge\n\n## Old\n\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, "- **b%02d** — body %d\n", i, i)
	}
	sb.WriteString("\n## Old: New\n")
	if err := os.WriteFile(km, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if _, _, err := s.TierSet(Umbrella, "Old", fmt.Sprintf("b%02d", i), Confirmed); err != nil {
			t.Fatal(err)
		}
	}
	parallel(n, func(i int) {
		r, err := s.RenameTopic(Umbrella, "Old", "Old: New", fmt.Sprintf("b%02d", i))
		if err != nil || !r.Changed {
			t.Errorf("b%02d: changed=%v err=%v", i, r.Changed, err)
		}
	})
	got, _ := os.ReadFile(km)
	oldStart := strings.Index(string(got), "## Old\n")
	newStart := strings.Index(string(got), "## Old: New")
	if oldStart < 0 || newStart < 0 || oldStart > newStart {
		t.Fatalf("headings missing or reordered:\n%s", got)
	}
	if left := string(got)[oldStart:newStart]; strings.Contains(left, "- **") {
		t.Errorf("bullets left under Old:\n%s", left)
	}
	sc, err := LoadSidecar(mustTier(t, s))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		title := fmt.Sprintf("b%02d", i)
		if !strings.Contains(string(got)[newStart:], "**"+title+"**") {
			t.Errorf("%s not moved in markdown", title)
		}
		if _, ok := sc.Get("Old: New", title); !ok {
			t.Errorf("%s not re-keyed under Old: New", title)
		}
		if _, ok := sc.Get("Old", title); ok {
			t.Errorf("%s still keyed under Old", title)
		}
	}
	if m, _ := filepath.Glob(km + ".*.tmp"); len(m) != 0 {
		t.Errorf("stray temp files: %v", m)
	}
}

func mustTier(t *testing.T, s Store) string {
	t.Helper()
	p, err := s.TierPath(Umbrella)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestGlossaryWriteKeepsEveryTermUnderConcurrency(t *testing.T) {
	s := lockedStore(t)
	const n = 12
	km, _ := s.KnowledgePath(Umbrella)
	if err := os.WriteFile(km, []byte("# Knowledge\n\n## Glossary\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	parallel(n, func(i int) {
		if _, _, err := s.GlossaryWrite(Umbrella, Term{Name: fmt.Sprintf("term%02d", i), Definition: "d"}, false, false); err != nil {
			t.Error(err)
		}
	})
	got, _ := os.ReadFile(km)
	for i := 0; i < n; i++ {
		if !strings.Contains(string(got), fmt.Sprintf("term%02d", i)) {
			t.Errorf("term%02d lost:\n%s", i, got)
		}
	}
}

// A hit buffered while RenameTopic waits for the KNOWLEDGE.md lock must be
// folded under that lock, or it lands keyed to the old topic after the re-key.
func TestRenameTopicFoldsHitsInsideItsLock(t *testing.T) {
	s := lockedStore(t)
	km, _ := s.KnowledgePath(Umbrella)
	if err := os.WriteFile(km, []byte("# Knowledge\n\n## Old\n\n- **b** — body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Hit(Umbrella, "Old", "b"); err != nil {
		t.Fatal(err)
	}
	hp, _ := s.HitsPath(Umbrella)

	held, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- fsio.Locked(km, fsio.LockTimeout, func() error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	renamed := make(chan error, 1)
	go func() {
		_, err := s.RenameTopic(Umbrella, "Old", "New", "")
		renamed <- err
	}()
	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(hp); err != nil {
		t.Errorf("RenameTopic folded the hit buffer before taking the lock: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-renamed; err != nil {
		t.Fatal(err)
	}
	sc, err := LoadSidecar(mustTier(t, s))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := sc.Get("New", "b"); !ok {
		t.Error("hit not re-keyed under New")
	}
	if _, ok := sc.Get("Old", "b"); ok {
		t.Error("hit still keyed under Old")
	}
}
