package worker

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/tracker"
)

type fakeIssueGet struct {
	is  tracker.Issue
	err error
}

func (f fakeIssueGet) Get(context.Context, tracker.IssueKey, bool) (tracker.Issue, error) {
	return f.is, f.err
}

func depthRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=a@b", "-c", "user.name=a"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("commit", "-q", "--allow-empty", "-m", "base")
	run("checkout", "-q", "-b", "feat")
	if err := os.WriteFile(filepath.Join(dir, "x.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "x.txt")
	run("commit", "-q", "-m", "change")
	return dir
}

func TestResolveReviewDepthSignals(t *testing.T) {
	dir := depthRepo(t)
	pol, err := config.ParseReviewPolicy(nil)
	if err != nil {
		t.Fatal(err)
	}
	pol.LightBelow = 100
	src := ReviewSources{Policy: pol, Git: git.Exec, Dir: dir, Span: "main...feat"}

	got, err := ResolveReviewDepth(context.Background(), src)
	if err != nil || got.Depth != config.DepthLight || got.Signal != config.SignalSize {
		t.Fatalf("size: %+v %v", got, err)
	}

	src.PRBody = func() (string, error) { return "Door: one-way. Blast radius: schema.", nil }
	if got, err = ResolveReviewDepth(context.Background(), src); err != nil || got.Depth != config.DepthFull || got.Signal != config.SignalDoor {
		t.Fatalf("door: %+v %v", got, err)
	}

	src.PRBody = nil
	src.Issue = tracker.NumKey(5)
	src.Issues = fakeIssueGet{is: tracker.Issue{Body: "## Files\n\n- y.txt\n"}}
	if got, err = ResolveReviewDepth(context.Background(), src); err != nil || got.Signal != config.SignalScopeDrift || !strings.Contains(got.Why, "x.txt") {
		t.Fatalf("drift: %+v %v", got, err)
	}

	src.Issues = fakeIssueGet{err: errors.New("boom")}
	var de *ReviewDepthError
	if _, err = ResolveReviewDepth(context.Background(), src); !errors.As(err, &de) || de.Stage != "issue" {
		t.Fatalf("unreadable issue must fail, not guess: %v", err)
	}
}

// A file moved out of a protected directory still counts as touching it.
func TestResolveReviewDepthSeesBothSidesOfARename(t *testing.T) {
	dir := depthRepo(t)
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=a@b", "-c", "user.name=a"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("checkout", "-q", "main")
	run("checkout", "-q", "-b", "mv")
	os.MkdirAll(filepath.Join(dir, "migrations"), 0o755)
	os.WriteFile(filepath.Join(dir, "migrations", "001.sql"), []byte("create table t (id int);\n"), 0o644)
	run("add", "migrations")
	run("commit", "-q", "-m", "add")
	run("checkout", "-q", "-b", "mv2")
	run("mv", "migrations/001.sql", "moved.sql")
	run("commit", "-q", "-m", "move")
	pol, err := config.ParseReviewPolicy(nil)
	if err != nil {
		t.Fatal(err)
	}
	pol.LightBelow = 1000
	pol.Paths = map[string]config.ReviewDepth{"migrations/**": config.DepthFull}
	got, err := ResolveReviewDepth(context.Background(), ReviewSources{Policy: pol, Git: git.Exec, Dir: dir, Span: "mv...mv2"})
	if err != nil || got.Signal != config.SignalPath {
		t.Fatalf("rename out of migrations/: %+v %v", got, err)
	}
}

// Git quotes a non-ASCII name in newline output; the -z list must match as is.
func TestResolveReviewDepthUnquotedPaths(t *testing.T) {
	dir := depthRepo(t)
	os.MkdirAll(filepath.Join(dir, "migrations"), 0o755)
	os.WriteFile(filepath.Join(dir, "migrations", "ä\"x.sql"), []byte("x\n"), 0o644)
	for _, args := range [][]string{{"add", "migrations"}, {"commit", "-q", "-m", "q"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=a@b", "-c", "user.name=a"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	pol, _ := config.ParseReviewPolicy(nil)
	pol.LightBelow = 1000
	pol.Paths = map[string]config.ReviewDepth{"migrations/**": config.DepthFull}
	got, err := ResolveReviewDepth(context.Background(), ReviewSources{Policy: pol, Git: git.Exec, Dir: dir, Span: "main...feat"})
	if err != nil || got.Signal != config.SignalPath || !strings.Contains(got.Why, "ä\"x.sql") {
		t.Fatalf("quoted name: %+v %v", got, err)
	}
}
