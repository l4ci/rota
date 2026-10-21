package hook

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/rotastate"
)

func TestNewestAtRoot(t *testing.T) {
	cd := t.TempDir()
	root := filepath.Join(t.TempDir(), "proj")
	other := filepath.Join(t.TempDir(), "elsewhere")
	for _, d := range []string{root, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	ts := func(d time.Duration) string { return base.Add(d).Format(time.RFC3339) }
	put := func(id, cwd, updated string) {
		t.Helper()
		path := filepath.Join(rotastate.SessionDir(cd), id+".json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := writeState(path, State{SessionID: id, Cwd: cwd, UpdatedAt: updated}); err != nil {
			t.Fatal(err)
		}
	}
	updated := func(st State) (string, bool) { return st.UpdatedAt, true }

	if _, _, ok := NewestAtRoot(cd, root, updated); ok {
		t.Fatal("no sessions must report none")
	}

	put("old", root, ts(-time.Hour))
	put("other-root", other, ts(0))
	put("bad-time", root, "not a time")
	put("new", root, ts(-time.Minute))
	st, at, ok := NewestAtRoot(cd, root, updated)
	if !ok || st.SessionID != "new" || !at.Equal(base.Add(-time.Minute)) {
		t.Fatalf("newest at the root wins, other roots and bad times skipped: %+v %v %v", st.SessionID, at, ok)
	}

	// at can skip a session: a newer one without the field does not mask an older one with it.
	only := func(id string) func(State) (string, bool) {
		return func(st State) (string, bool) { return st.UpdatedAt, st.SessionID == id }
	}
	if st, _, ok := NewestAtRoot(cd, root, only("old")); !ok || st.SessionID != "old" {
		t.Fatalf("skipped sessions must not mask older ones: %+v %v", st.SessionID, ok)
	}
	if _, _, ok := NewestAtRoot(cd, root, only("other-root")); ok {
		t.Fatal("a session at another root must not match")
	}
	if _, _, ok := NewestAtRoot(cd, root, only("bad-time")); ok {
		t.Fatal("an unparsable time is no evidence")
	}

	// A tie keeps one of the tied sessions deterministically-enough: either is the same instant.
	put("tie-a", root, ts(time.Hour))
	put("tie-b", root, ts(time.Hour))
	if st, at, ok := NewestAtRoot(cd, root, updated); !ok || !at.Equal(base.Add(time.Hour)) || (st.SessionID != "tie-a" && st.SessionID != "tie-b") {
		t.Fatalf("a tie returns one of the tied sessions: %+v %v %v", st.SessionID, at, ok)
	}
}
