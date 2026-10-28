package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/rotastatetest"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/roundtick"
	"github.com/l4ci/rota/internal/verdict"
)

// Existing nudge tests exercise the legacy policy explicitly.
func trainNudges(waiting []waitingPR) []trainNudge {
	return trainNudgesWithFill(waiting, roundcfg.TrainFill{}, time.Time{})
}

func TestTrainNudgeExcludesAutopilotHolds(t *testing.T) {
	for _, tc := range []struct {
		name   string
		held   map[string]string
		failed map[string]roundtick.FailedHead
		want   int
	}{
		{name: "legacy held slot", held: map[string]string{"ben": "verify-failed: red"}, want: 1},
		{name: "legacy held PR", held: map[string]string{"#11": "verify-failed: red"}, want: 1},
		{name: "failed head follows queued PR", failed: map[string]roundtick.FailedHead{"old-slot": {Head: "current", Why: "verify-failed: red"}}, want: 1},
		{name: "new head clears failure", failed: map[string]roundtick.FailedHead{"ben": {Head: "11:previous-head", Why: "verify-failed: red"}}, want: 2},
		{name: "transient is not held", held: map[string]string{"ben": "forge: unavailable"}, want: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := nudgeProject(t, []string{"ben/1-x", "dana/2-x"}, []string{"ben/1-x", "dana/2-x"})
			write(t, filepath.Join(root, ".rota", "workers.json"), `{"round":40,"slots":[{"name":"ben","branch":"ben/1-x","state":"done","pr":"#11"},{"name":"dana","branch":"dana/2-x","state":"done","pr":"#12"}]}`)
			d := testDeps()
			r, err := d.Git(context.Background(), root, "rev-parse", "HEAD")
			if err != nil || r.ExitCode != 0 {
				t.Fatalf("head: %+v %v", r, err)
			}
			if prev, ok := tc.failed["old-slot"]; ok {
				prev.Head = "11:" + strings.TrimSpace(r.Stdout)
				tc.failed["old-slot"] = prev
			}
			cd, err := rotastatetest.CommonDir(root)
			if err != nil {
				t.Fatal(err)
			}
			if err := roundtick.SaveState(cd, 40, roundtick.Result{Held: tc.held, FailedHeads: tc.failed}); err != nil {
				t.Fatal(err)
			}
			ready := 0
			for _, w := range waitingPRs(&Ctx{Deps: d, ctx: context.Background()}, root) {
				if w.Reviewed {
					ready++
				}
			}
			if ready != tc.want {
				t.Fatalf("ready=%d, want %d", ready, tc.want)
			}
		})
	}
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

func TestTrainFillReviewDisabledUsesDoneTime(t *testing.T) {
	for _, policy := range []string{`false`, `"none"`} {
		t.Run(policy, func(t *testing.T) {
			root := nudgeProject(t, []string{"ben/1-x"}, nil)
			write(t, filepath.Join(root, ".rota", "config.json"), `{"ship":{"review":`+policy+`},"round":{"trainMax":3,"trainFillMinutes":60}}`)
			addRegistryRows(t, root, map[string][]map[string]any{"slots": {{
				"name": "ben", "branch": "ben/1-x", "state": "done", "pr": "#11", "base": "main",
				"event": map[string]any{"kind": "done", "ts": "2026-10-27T08:00:00Z"},
			}}})
			d := testDeps()
			pinNow(d, "2026-10-27T09:00:00Z")
			c := &Ctx{Deps: d, ctx: context.Background()}
			if ns := readyTrainNudges(c, root); len(ns) != 1 || len(ns[0].PRs) != 1 {
				t.Fatalf("review disabled: nudges=%+v, want lone PR due from its done time", ns)
			}
		})
	}
}

func TestTrainFillUnclockedReviewDisabledPR(t *testing.T) {
	root := nudgeProject(t, []string{"ben/1-x"}, nil)
	write(t, filepath.Join(root, ".rota", "config.json"), `{"ship":{"review":false},"round":{"trainMax":4,"trainFillMinutes":20}}`)
	addRegistryRows(t, root, map[string][]map[string]any{"prs": {{"branch": "ben/1-x", "pr": "#11", "base": "main"}}})
	if ns := readyTrainNudges(&Ctx{Deps: testDeps(), ctx: context.Background()}, root); len(ns) != 1 {
		t.Fatalf("unclocked ready PR must land immediately: %+v", ns)
	}
}

func TestTrainFillImmediateNudgeLeavesFreshPRsFilling(t *testing.T) {
	now := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	ns := trainNudgesWithFill([]waitingPR{
		{PR: "#1", Base: "main", Reviewed: true, ReadySince: now.Add(-time.Minute)},
		{PR: "#90", Base: "main", Reviewed: true, Immediate: true},
	}, roundcfg.TrainFill{Max: 4, Minutes: 20}, now)
	if len(ns) != 1 || ns[0].Command() != "rota worker gate 90 --base main" {
		t.Fatalf("unclocked fallback also released fresh PRs: %+v", ns)
	}
}

func TestTrainNudgeKeepsOldestAndShowsObservedWait(t *testing.T) {
	now := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	ns := trainNudgesWithFill([]waitingPR{
		{PR: "#1", Base: "main", Reviewed: true, ReadySince: now.Add(-time.Minute)},
		{PR: "#2", Base: "main", Reviewed: true, ReadySince: now.Add(-2 * time.Minute)},
		{PR: "#90", Base: "main", Reviewed: true, ReadySince: now.Add(-65 * time.Minute)},
	}, roundcfg.TrainFill{Max: 2, Minutes: 60}, now)
	if len(ns) != 1 || ns[0].Command() != "rota worker train 2 90 --base main" {
		t.Fatalf("oldest-ready selection: %+v", ns)
	}
	for _, part := range []string{"2 of 3 ready PRs", "oldest waited 65m of 60m"} {
		if !strings.Contains(ns[0].Text(), part) {
			t.Errorf("text %q missing %q", ns[0].Text(), part)
		}
	}
}

func TestTrainNudgeInvalidUnrelatedConfigKeepsLegacy(t *testing.T) {
	root := nudgeRepo(t, 2)
	write(t, filepath.Join(root, ".rota", "config.json"), `{"round":{"maxBounces":"bad"}}`)
	if ns := readyTrainNudges(&Ctx{Deps: testDeps(), ctx: context.Background()}, root); len(ns) != 1 {
		t.Fatalf("unrelated invalid config hid legacy nudge: %+v", ns)
	}
}
