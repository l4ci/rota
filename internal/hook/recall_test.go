package hook

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/knowledge"
	"github.com/l4ci/rota/internal/rotastate"
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

func TestSweepRemovesExpiredRecallReceiptsAndKeepsLive(t *testing.T) {
	cd := t.TempDir()
	t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	entry := []knowledge.PathEntry{{ID: "one", Text: "guidance"}}
	for _, id := range []string{"old", "live"} {
		if Recall(cd, id, entry, t0) == "" {
			t.Fatalf("recall %s wrote no receipt", id)
		}
	}
	recallDir := filepath.Join(rotastate.SessionDir(cd), "recall")
	age := func(id string, at time.Time) {
		t.Helper()
		if err := os.Chtimes(filepath.Join(recallDir, id+".json"), at, at); err != nil {
			t.Fatal(err)
		}
	}
	now := t0.Add(RecallTTL + time.Hour)
	age("old", t0)
	age("live", now.Add(-time.Hour))
	if err := Dump(cd, mustPayload(t, []byte(`{"session_id":"cur"}`)), now); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"old.json", "old.json.lock"} {
		if _, err := os.Stat(filepath.Join(recallDir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s survived the sweep: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(recallDir, "live.json")); err != nil {
		t.Fatalf("live receipt removed: %v", err)
	}
	// Receipts are not statusline state.
	if _, ok := reflect.TypeOf(State{}).FieldByName("RecalledEntries"); ok {
		t.Fatal("State still carries RecalledEntries")
	}
}
