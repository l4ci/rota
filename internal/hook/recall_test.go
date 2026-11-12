package hook

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/knowledge"
)

func TestDecideRecall(t *testing.T) {
	root, commonDir := t.TempDir(), t.TempDir()
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, tool, file, cwd  string
		entriesErr, commonErr  bool
		noEntries, wantContext bool
	}{
		{name: "relative edit", tool: "Edit", file: "internal/x.go", cwd: root, wantContext: true},
		{name: "absolute write with fallback cwd", tool: "Write", file: filepath.Join(root, "internal/x.go"), wantContext: true},
		{name: "other tool", tool: "Bash", file: "internal/x.go"},
		{name: "missing path", tool: "Edit"},
		{name: "query failure", tool: "Edit", file: "internal/x.go", entriesErr: true},
		{name: "no matches", tool: "Write", file: "internal/x.go", noEntries: true},
		{name: "common dir failure", tool: "Write", file: "internal/x.go", commonErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := RecallRequest{
				Payload: Payload{SessionID: strings.ReplaceAll(tc.name, " ", "-"), Cwd: tc.cwd, Fields: map[string]any{
					"tool_name": tc.tool, "tool_input": map[string]any{"file_path": tc.file},
				}},
				DefaultCwd: root,
				Entries: func(cwd, file string) ([]knowledge.PathEntry, error) {
					if cwd != root || file != filepath.Join(root, "internal/x.go") {
						t.Fatalf("query cwd=%q file=%q", cwd, file)
					}
					if tc.entriesErr {
						return nil, errors.New("unreadable knowledge")
					}
					if tc.noEntries {
						return nil, nil
					}
					return []knowledge.PathEntry{{ID: "one", Text: "Keep the seam."}}, nil
				},
				CommonDir: func(cwd string) (string, error) {
					if cwd != root {
						t.Fatalf("common dir cwd=%q", cwd)
					}
					if tc.commonErr {
						return "", errors.New("git unavailable")
					}
					return commonDir, nil
				},
				Now: func() time.Time { return now },
			}
			want := ""
			if tc.wantContext {
				want = "Path-scoped project context (before editing):\n\nKeep the seam."
			}
			if got := DecideRecall(in); got != want {
				t.Fatalf("context=%q, want %q", got, want)
			}
			if got := DecideRecall(in); got != "" {
				t.Fatalf("repeated context=%q", got)
			}
		})
	}
}

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
	if err := Dump(dir, mustPayload(t, []byte(`{"session_id":"session","cwd":"/project"}`)), now); err != nil {
		t.Fatal(err)
	}
	if got := Recall(dir, "session", entry, now); got != "" {
		t.Fatalf("refresh forgot recall: %s", got)
	}
	// Another session's statusline cleanup must not forget a still-open
	// session's already-injected entries, even after the readings expire.
	if err := Dump(dir, mustPayload(t, []byte(`{"session_id":"other","cwd":"/project"}`)), now.Add(25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := Recall(dir, "session", entry, now.Add(25*time.Hour)); got != "" {
		t.Fatalf("reading cleanup forgot recall: %s", got)
	}
	entry = append(entry, knowledge.PathEntry{ID: "two", Text: "new guidance"})
	if got := Recall(dir, "session", entry, now); !strings.Contains(got, "new guidance") || strings.Contains(got, "unique guidance") {
		t.Fatalf("new entry: %s", got)
	}
	if got := Recall(dir, "../bad", entry, now); got != "" {
		t.Fatalf("invalid session injected: %s", got)
	}
}
