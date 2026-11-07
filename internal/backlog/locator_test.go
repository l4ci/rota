package backlog

import (
	"errors"
	"strings"
	"testing"
)

// TestFileRefLocator drives every file-backend site that finds a bullet by ref
// through the Backend and the exported readers: one ref only ever matches its
// own bullet, never a longer ref that shares its prefix (B1 vs B10), never a
// regex reading of the ref, and open, struck and archived forms agree.
func TestFileRefLocator(t *testing.T) {
	const backlog = "# TODO\n\n## Bugs\n- **[B1] [P1] Short.** x\n- **[B10] [P1] Long.** y\n\n## Completed\n"
	f, _ := proj(t, map[string]string{".rota/BACKLOG.md": backlog})
	var be Backend = f

	if it, err := be.Get("B1"); err != nil || it.Title != "Short" {
		t.Fatalf("Get B1 = %+v, %v", it, err)
	}
	if !IsActive(f.Root, "B10") || IsActive(f.Root, "B.") {
		t.Fatal("IsActive must match the literal ref only")
	}
	if _, err := be.Get("B."); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get B. = %v; the ref is not a regex", err)
	}
	if _, err := be.Complete("B.", CompleteInput{Reason: "dropped"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Complete B. = %v; the ref is not a regex", err)
	}

	if ok, err := be.Complete("B1", CompleteInput{Commit: "abc", Date: "2026-10-06", Reason: "dropped"}); err != nil || !ok {
		t.Fatalf("Complete B1 = %v, %v", ok, err)
	}
	got := read(t, f, ".rota/BACKLOG.md")
	if !strings.Contains(got, "- **[B10] [P1] Long.** y\n") || !strings.Contains(got, "- ~~**[B1] [P1] Short.** x~~ Done 2026-10-06") {
		t.Fatalf("Complete B1 touched the wrong bullet:\n%s", got)
	}
	if done, err := be.Get("B1"); err != nil || !done.Closed || done.Reason != "dropped" {
		t.Fatalf("Get B1 after Complete = %+v, %v", done, err)
	}
	if again, err := be.Complete("B1", CompleteInput{Reason: "dropped"}); err != nil || again {
		t.Fatalf("Complete B1 twice = %v, %v; want a no-op", again, err)
	}
	if ok, err := be.Reopen("B1"); err != nil || !ok {
		t.Fatalf("Reopen B1 = %v, %v", ok, err)
	}
	if got := read(t, f, ".rota/BACKLOG.md"); !strings.Contains(got, "- **[B1] [P1] Short.** x\n") {
		t.Fatalf("Reopen B1:\n%s", got)
	}
	if _, err := f.SetHarness("B10", "codex"); err != nil {
		t.Fatal(err)
	}
	got = read(t, f, ".rota/BACKLOG.md")
	if !strings.Contains(got, "- **[B1] [P1] Short.** x\n") || strings.Count(got, "Harness:") != 1 {
		t.Fatalf("SetHarness B10 touched B1:\n%s", got)
	}
}
