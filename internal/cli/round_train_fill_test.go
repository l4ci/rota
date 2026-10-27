package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/verdict"
)

// Existing nudge tests exercise the legacy policy explicitly.
func trainNudges(waiting []waitingPR) []trainNudge {
	return trainNudgesWithFill(waiting, roundcfg.TrainFill{}, time.Time{})
}

func TestConfiguredTrainFillWaits(t *testing.T) {
	root := nudgeRepo(t, 2)
	write(t, filepath.Join(root, ".rota", "config.json"), `{"round":{"trainMax":3,"trainFillMinutes":60}}`)
	c := &Ctx{Deps: testDeps(), ctx: context.Background()}
	if ns := readyTrainNudges(c, root); len(ns) != 0 {
		t.Fatalf("nudges = %+v, want none before 3 PRs or 60 minutes", ns)
	}
}

func TestConfiguredTrainFillCount(t *testing.T) {
	root := nudgeRepo(t, 4)
	write(t, filepath.Join(root, ".rota", "config.json"), `{"round":{"trainMax":3,"trainFillMinutes":60}}`)
	ns := readyTrainNudges(&Ctx{Deps: testDeps(), ctx: context.Background()}, root)
	if len(ns) != 1 || len(ns[0].PRs) != 3 {
		t.Fatalf("nudges = %+v, want a train capped at 3 PRs", ns)
	}
	if got := ns[0].Command(); got != "rota worker train 11 12 13 --base feat/x" {
		t.Errorf("command = %q", got)
	}
	const reason = "round.trainMax reached (3 PRs)"
	if !strings.Contains(ns[0].Text(), reason) {
		t.Errorf("text lacks firing rule: %s", ns[0].Text())
	}
	if got, _ := trainNudgeData(ns)[0].(*jsonx.Object).Get("reason"); got != reason {
		t.Errorf("JSON reason = %v", got)
	}
}

func TestConfiguredTrainFillDeadline(t *testing.T) {
	root := nudgeRepo(t, 1)
	write(t, filepath.Join(root, ".rota", "config.json"), `{"round":{"trainMax":3,"trainFillMinutes":60}}`)
	d := testDeps()
	pinNow(d, "2026-10-27T09:00:00Z")
	r, err := d.Git(context.Background(), root, "rev-parse", "HEAD")
	if err != nil || r.ExitCode != 0 {
		t.Fatalf("head: %+v %v", r, err)
	}
	at, _ := time.Parse(time.RFC3339, "2026-10-27T08:00:00Z")
	rec := verdict.NewRecord(verdict.ReviewSpec, verdict.Pass, strings.TrimSpace(r.Stdout), verdict.Body{}, at)
	if _, err := verdict.AddBranch(root, verdict.BranchKey("", "w1/1-x"), rec); err != nil {
		t.Fatal(err)
	}
	ns := readyTrainNudges(&Ctx{Deps: d, ctx: context.Background()}, root)
	if len(ns) != 1 {
		t.Fatalf("nudges = %+v, want lone PR due at 60 minutes", ns)
	}
	if got := ns[0].Command(); got != "rota worker gate 11 --base feat/x" {
		t.Errorf("command = %q", got)
	}
	if got := ns[0].Text(); !strings.Contains(got, "round.trainFillMinutes reached (60 minutes)") {
		t.Errorf("text lacks firing rule: %s", got)
	}
	// The second review finishes later: age starts when that prerequisite
	// finishes, rather than when the earlier spec review was recorded.
	rec = verdict.NewRecord(verdict.ReviewQuality, verdict.Pass, rec.Sha, verdict.Body{}, at.Add(30*time.Minute))
	if _, err := verdict.AddBranch(root, verdict.BranchKey("", "w1/1-x"), rec); err != nil {
		t.Fatal(err)
	}
	if ns := readyTrainNudges(&Ctx{Deps: d, ctx: context.Background()}, root); len(ns) != 0 {
		t.Fatalf("nudges = %+v, want wait for latest current-head review", ns)
	}
	pinNow(d, "2026-10-27T09:30:00Z")
	// A later review of another head must not reset this head's deadline.
	rec = verdict.NewRecord(verdict.ReviewSecurity, verdict.Pass, "deadbeef", verdict.Body{}, at.Add(2*time.Hour))
	if _, err := verdict.AddBranch(root, verdict.BranchKey("", "w1/1-x"), rec); err != nil {
		t.Fatal(err)
	}
	if ns := readyTrainNudges(&Ctx{Deps: d, ctx: context.Background()}, root); len(ns) != 1 {
		t.Fatalf("nudges = %+v, want deadline independent of another head's review", ns)
	}
}
