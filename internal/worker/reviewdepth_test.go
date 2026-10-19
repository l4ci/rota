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
