package roundwatch

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/rotastate"
)

func TestSeenStoreStartsEmpty(t *testing.T) {
	if got := NewSeenStore(t.TempDir()).Seen(); got == nil || len(got) != 0 {
		t.Fatalf("fresh seen = %#v", got)
	}
}

// Mark merges, never drops, and a later store over the same dir sees it.
func TestSeenStoreMarkMergesAndPersists(t *testing.T) {
	cd := t.TempDir()
	s := NewSeenStore(cd)
	s.Mark(map[string]string{"support/7/pr-head": "aaa"})
	s.Mark(map[string]string{"support/8/comment": "ann#2"})
	s.Mark(map[string]string{"support/7/pr-head": "bbb"})
	got := NewSeenStore(cd).Seen()
	if len(got) != 2 || got["support/7/pr-head"] != "bbb" || got["support/8/comment"] != "ann#2" {
		t.Fatalf("seen = %v", got)
	}
	if _, err := os.Stat(rotastate.File(cd, SeenFile)); err != nil {
		t.Errorf("seen file not under the rota state dir: %v", err)
	}
}

// A corrupt record reads as empty and the next Mark replaces it.
func TestSeenStoreCorruptFileReadsEmpty(t *testing.T) {
	cd := t.TempDir()
	p := rotastate.File(cd, SeenFile)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewSeenStore(cd)
	if len(s.Seen()) != 0 {
		t.Fatalf("corrupt read = %v", s.Seen())
	}
	s.Mark(map[string]string{"k": "v"})
	if got := s.Seen(); len(got) != 1 || got["k"] != "v" {
		t.Fatalf("after mark = %v", got)
	}
}
