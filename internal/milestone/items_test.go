package milestone

import (
	"os"
	"strings"
	"testing"
)

func itemsProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(detailPath(root, "M01")[:strings.LastIndex(detailPath(root, "M01"), "/")], 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(detailPath(root, "M01"), []byte(StubOn("M01", "One", "s", nil, "2026-01-01")), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestSyncItems(t *testing.T) {
	items := []ItemRef{
		{ID: "F01", Title: "First [x]", Detail: ".rota/features/F01.md"},
		{ID: "B02", Title: "Second"},
	}
	t.Run("adds a linked list and is idempotent", func(t *testing.T) {
		root := itemsProject(t)
		changed, err := SyncItems(root, "M01", items)
		if err != nil || !changed {
			t.Fatalf("changed=%v err=%v", changed, err)
		}
		got := read(t, detailPath(root, "M01"))
		if !strings.Contains(got, "## Items\n") || !strings.Contains(got, "- [[F01] First \\[x\\]](../features/F01.md)\n") || !strings.Contains(got, "- [B02] Second\n") {
			t.Fatalf("section missing:\n%s", got)
		}
		if changed, err = SyncItems(root, "M01", items); err != nil || changed {
			t.Fatalf("second run changed=%v err=%v", changed, err)
		}
	})
	t.Run("keeps text outside the section", func(t *testing.T) {
		root := itemsProject(t)
		p := detailPath(root, "M01")
		if _, err := SyncItems(root, "M01", items); err != nil {
			t.Fatal(err)
		}
		withTail := read(t, p) + "\n## After\n\nhand-written\n"
		if err := os.WriteFile(p, []byte(withTail), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := SyncItems(root, "M01", items[:1]); err != nil {
			t.Fatal(err)
		}
		got := read(t, p)
		if strings.Contains(got, "Second") || !strings.Contains(got, "\n## After\n\nhand-written\n") || !strings.Contains(got, "## Goal") {
			t.Fatalf("outside text lost or stale item kept:\n%s", got)
		}
	})
	t.Run("no items and no section writes nothing", func(t *testing.T) {
		root := itemsProject(t)
		before := read(t, detailPath(root, "M01"))
		if changed, err := SyncItems(root, "M01", nil); err != nil || changed || read(t, detailPath(root, "M01")) != before {
			t.Fatalf("changed=%v err=%v", changed, err)
		}
	})
	t.Run("emptied list keeps the section", func(t *testing.T) {
		root := itemsProject(t)
		SyncItems(root, "M01", items)
		if changed, err := SyncItems(root, "M01", nil); err != nil || !changed {
			t.Fatalf("changed=%v err=%v", changed, err)
		}
		if got := read(t, detailPath(root, "M01")); !strings.Contains(got, "## Items") || strings.Contains(got, "F01") {
			t.Fatalf("got:\n%s", got)
		}
	})
	t.Run("unknown milestone", func(t *testing.T) {
		if _, err := SyncItems(t.TempDir(), "M09", items); exitOf(err) != 3 {
			t.Fatalf("err = %v", err)
		}
	})
}
