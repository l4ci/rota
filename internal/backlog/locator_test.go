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
	if _, err := f.SetField("B10", "related", "[F01]"); err != nil {
		t.Fatal(err)
	}
	got = read(t, f, ".rota/BACKLOG.md")
	if !strings.Contains(got, "- **[B1] [P1] Short.** x\n") || strings.Count(got, "Related:") != 1 {
		t.Fatalf("SetField B10 touched B1:\n%s", got)
	}
}

// TestBulletGrammarMatchers pins which of the line grammars (OpenRe, doneRe,
// archiveBulletRe) accepts each bullet shape. The grammars are line-based, so
// a bullet inside a code fence reads as a plain line.
func TestBulletGrammarMatchers(t *testing.T) {
	const doneBase = "- ~~**[B07] x**~~ Done 2026-01-01 [`abc1234`]"
	rows := []struct {
		name               string
		line               string
		open, done, archiv bool
	}{
		{"open", "- **[B07] x**", true, false, true},
		{"open tagged", "- **[B07] [P1] Title.** rest", true, false, true},
		{"done", doneBase, false, true, true},
		{"done dropped with note", doneBase + " (dropped: note)", false, true, true},
		{"done handed-off", doneBase + " (handed-off)", false, true, true},
		{"done unknown reason", doneBase + " (parked)", false, false, true},
		{"struck archive prefix", "- ~~**[B07]", false, false, true},
		{"plain archive prefix", "- **[B07]", false, false, true},
		{"slice id archive", "- **[S1] x**", false, false, true},
		{"indented bullet", "  - **[B07] x**", false, false, false},
		{"fenced bullet reads as plain line", "- **[B07] x**", true, false, true},
		{"code fence marker", "```", false, false, false},
		{"non-id bold bullet", "- **[note] x**", false, false, false},
		{"plain bullet", "- B07 x", false, false, false},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			if got := OpenRe.MatchString(r.line); got != r.open {
				t.Errorf("OpenRe(%q) = %v, want %v", r.line, got, r.open)
			}
			if got := doneRe.MatchString(r.line); got != r.done {
				t.Errorf("doneRe(%q) = %v, want %v", r.line, got, r.done)
			}
			if got := archiveBulletRe.MatchString(r.line); got != r.archiv {
				t.Errorf("archiveBulletRe(%q) = %v, want %v", r.line, got, r.archiv)
			}
		})
	}
}
