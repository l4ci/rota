package extknowledge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
)

// failingHook installs a pre-commit hook that appends to other (a tracked
// file the plan does not own) and then fails, so the commit fails after the
// clone passed its clean check.
func failingHook(t *testing.T, f *fixture, other string) {
	t.Helper()
	hook := filepath.Join(f.a, ".git", "hooks", "pre-commit")
	script := "#!/bin/sh\necho edited-concurrently >> " + other + "\nexit 1\n"
	if err := os.WriteFile(hook, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestGitPathsAreLiteral(t *testing.T) {
	f := newFixture(t, "")
	gittest.Write(t, f.a, "topics/untracked.md", "mine\n")
	// A plan whose path holds glob characters touches only that exact path.
	f.clone().rollback(context.Background(), &plan{order: []string{"topics/*.md"}, deletes: []string{"topics/?o.md"}})
	if _, err := os.Stat(filepath.Join(f.a, "topics/untracked.md")); err != nil {
		t.Fatalf("a glob path matched another file: %v", err)
	}
}

func TestCraftedIndexLinesAreIgnoredAndReported(t *testing.T) {
	f := newFixture(t, "")
	gittest.Commit(t, f.a, "crafted", "README.md", seedIndex+
		"- [X](topics/*.md) — glob\n- [Dots](topics/../README.md) — up\n- [Q](topics/a?b.md) — q\n")
	gittest.Write(t, f.a, "topics/untracked.md", "mine\n")
	c := f.clone()
	if got := topicNames(t, c); len(got) != 1 || got[0] != "Go" {
		t.Fatalf("topics = %v", got)
	}
	bad := c.Malformed()
	if len(bad) != 3 || !strings.Contains(bad[0], "topics/*.md") || !strings.Contains(bad[1], "../README.md") {
		t.Fatalf("Malformed = %q", bad)
	}
	if _, err := c.QueryWith([]string{"X"}, QueryOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.a, "topics/untracked.md")); err != nil {
		t.Fatal(err)
	}
}

func TestCraftedSubIndexLineIsIgnoredAndReported(t *testing.T) {
	f := splitFixture(t, "")
	gittest.Commit(t, f.a, "crafted", "topics/godot/README.md", splitReadme+"\n- [Up](../../README.md) — x\n- [Glob](*.md) — y\n")
	c := f.clone()
	for _, n := range topicNames(t, c) {
		if strings.Contains(n, "Up") || strings.Contains(n, "Glob") {
			t.Fatalf("followed %q", n)
		}
	}
	if len(c.Malformed()) != 2 {
		t.Fatalf("Malformed = %q", c.Malformed())
	}
}

func TestFailedCommitRestoresOnlyThePlansFiles(t *testing.T) {
	f := newFixture(t, "")
	gittest.Commit(t, f.a, "other", "NOTES.md", "notes\n")
	gittest.Run(t, f.a, "push", "-q")
	gittest.Write(t, f.a, "keep/untracked.md", "mine\n")
	failingHook(t, f, "NOTES.md")
	_, err := f.clone().Add(context.Background(), AddIn{Topic: "Fresh", Title: "t", Body: "b.", Date: "2026-10-25"})
	if err == nil {
		t.Fatal("commit must fail")
	}
	notes, _ := os.ReadFile(filepath.Join(f.a, "NOTES.md"))
	if string(notes) != "notes\nedited-concurrently\n" {
		t.Fatalf("an unrelated edit was reset: %q", notes)
	}
	if st := gittest.Run(t, f.a, "status", "--porcelain"); st != "M NOTES.md\n?? keep/" && strings.TrimSpace(st) != "M NOTES.md\n?? keep/" {
		t.Fatalf("status = %q", st)
	}
	if _, err := os.Stat(filepath.Join(f.a, "topics/fresh.md")); !os.IsNotExist(err) {
		t.Fatalf("the plan's new file remains: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(f.a, "README.md")); string(got) != seedIndex {
		t.Fatalf("index not restored: %q", got)
	}
	// With the hook gone and the foreign edit committed away, the next write works.
	_ = os.Remove(filepath.Join(f.a, ".git", "hooks", "pre-commit"))
	gittest.Run(t, f.a, "checkout", "--", "NOTES.md")
	if _, err := f.clone().Add(context.Background(), AddIn{Topic: "Fresh", Title: "t", Body: "b.", Date: "2026-10-25"}); err != nil {
		t.Fatalf("next write: %v", err)
	}
}

func TestSplitRefusesUntrackedStray(t *testing.T) {
	f := splitFixture(t, "")
	gittest.Commit(t, f.a, "older rota root line", "README.md", seedIndex+splitRootAdd+"- [Godot](topics/godot.md) — engine\n")
	gittest.Write(t, f.a, "topics/godot.md", strayGodot)
	n := commitCount(t, f)
	_, _, err := f.clone().Split(context.Background(), SplitIn{Topic: "Godot"})
	if !errorsIs(err, ErrDirty) || !strings.Contains(err.Error(), "topics/godot.md") {
		t.Fatalf("err = %v", err)
	}
	if _, serr := os.Stat(filepath.Join(f.a, "topics/godot.md")); serr != nil || commitCount(t, f) != n {
		t.Fatal("a refused split changed the clone")
	}
}

func TestAddSubtopicUnderUnknownTopicRefused(t *testing.T) {
	f := splitFixture(t, "")
	n := commitCount(t, f)
	_, err := f.clone().Add(context.Background(), AddIn{Topic: "Unknown: Sub", Title: "x", Body: "y.", Date: "2026-10-25"})
	if !errorsIs(err, ErrNotFound) || !strings.Contains(err.Error(), "add to") {
		t.Fatalf("err = %v", err)
	}
	if commitCount(t, f) != n {
		t.Fatal("a refused add wrote a commit")
	}
}

func TestHitsLogIsPrivate(t *testing.T) {
	f := newFixture(t, "")
	c := f.clone()
	if _, err := c.Hit("go", "table tests", "game"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(c.hitsPath())
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file = %v, %v", fi, err)
	}
	di, err := os.Stat(filepath.Dir(c.hitsPath()))
	if err != nil || di.Mode().Perm() != 0o700 {
		t.Fatalf("dir = %v, %v", di, err)
	}
}
