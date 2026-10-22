package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/backlog/trackertest"
)

// The clock seams (Deps.Today / Deps.Now) are wired from the CLI edge into
// stores that default to the wall clock when the field is nil. These tests
// pin a day no wall clock will report and assert the stamped date, so removing
// a wiring line (Today: deps.Today, u.Today = opts.Today, debugctr.Open(root,
// deps.Now), ...) fails here and not only in production.
const seamDay = "2031-02-03"

func seamDeps(deps *Deps) *Deps {
	pinDay(deps, seamDay)
	deps.Now = func() time.Time { return time.Date(2031, 2, 3, 4, 5, 6, 0, time.UTC) }
	return deps
}

func TestMilestoneAddStampsTheDepsDayFileMode(t *testing.T) {
	dir := gitRepo(t)
	code, _, stderr := rotaInWith(t, seamDeps(testDeps()), dir, "milestone", "add", "--title", "Dated", "--summary", "S")
	if code != 0 {
		t.Fatalf("add: exit %d: %s", code, stderr)
	}
	b, err := os.ReadFile(filepath.Join(dir, ".rota", "milestones", "M01.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "created: "+seamDay+"\n") {
		t.Errorf("M01 lacks the pinned created date:\n%s", b)
	}
}

func TestMilestoneAddStampsTheDepsDayIssueMode(t *testing.T) {
	root := trackerProject(t, issuesConfig)
	fake := msFixture()
	deps := seamDeps(withTracker(t, fake))
	if code, _, stderr := issueRunWith(t, deps, root, "milestone", "add", "--title", "Third", "--summary", "S3."); code != 0 {
		t.Fatalf("add: exit %d: %s", code, stderr)
	}
	assertCreatedIssueDay(t, fake.Issues, "M03 — Third")
}

func TestMilestoneAddStampsTheDepsDayUmbrella(t *testing.T) {
	root, deps, fakes, _ := umbrellaProject(t)
	seamDeps(deps)
	// milestone add needs native milestones, which the plain Fake lacks.
	subs := map[string]*trackertest.MS{}
	for name, f := range fakes {
		subs[name] = &trackertest.MS{Fake: f}
	}
	deps.NewTracker = func(_ context.Context, dir string, _ any) (backlog.Tracker, error) {
		return subs[filepath.Base(dir)], nil
	}
	if code, _, stderr := rotaRunWith(t, deps, "--json", "-C", root, "milestone", "add", "--title", "Next", "--summary", "S."); code != 0 {
		t.Fatalf("add: exit %d: %s", code, stderr)
	}
	assertCreatedIssueDay(t, fakes["web"].Issues, "M01 — Next")
}

func assertCreatedIssueDay(t *testing.T, issues []backlog.Issue, title string) {
	t.Helper()
	for _, is := range issues {
		if is.Title == title {
			if !strings.Contains(is.Body, "created: "+seamDay+"\n") {
				t.Errorf("%s lacks the pinned created date:\n%s", title, is.Body)
			}
			return
		}
	}
	t.Fatalf("no issue titled %q in %+v", title, issues)
}

func TestDebugCounterInitStampsTheDepsNow(t *testing.T) {
	dir := gitRepo(t)
	code, _, stderr := rotaInWith(t, seamDeps(testDeps()), dir, "debug", "counter", "init", "B07")
	if code != 0 {
		t.Fatalf("init: exit %d: %s", code, stderr)
	}
	b, err := os.ReadFile(filepath.Join(dir, ".rota", "debug", "feat-x.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"started_at": "2031-02-03T04:05:06`) {
		t.Errorf("counter lacks the pinned started_at:\n%s", b)
	}
}
