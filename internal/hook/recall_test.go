package hook

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/knowledge"
)

func TestRecallClaimsConcurrentEntriesAndPreservesRefresh(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	entry := []knowledge.PathEntry{{ID: "one", Text: "unique guidance"}}
	var wg sync.WaitGroup
	out := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); out <- Recall(dir, "session", entry, now) }()
	}
	wg.Wait()
	close(out)
	count := 0
	for s := range out {
		if strings.Contains(s, "unique guidance") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("injected %d times, want 1", count)
	}
	if err := Dump(dir, []byte(`{"session_id":"session","cwd":"/project"}`), now); err != nil {
		t.Fatal(err)
	}
	if got := Recall(dir, "session", entry, now); got != "" {
		t.Fatalf("refresh forgot recall: %s", got)
	}
	entry = append(entry, knowledge.PathEntry{ID: "two", Text: "new guidance"})
	if got := Recall(dir, "session", entry, now); !strings.Contains(got, "new guidance") || strings.Contains(got, "unique guidance") {
		t.Fatalf("new entry: %s", got)
	}
	if got := Recall(dir, "../bad", entry, now); got != "" {
		t.Fatalf("invalid session injected: %s", got)
	}
}
