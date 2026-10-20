package cli

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/roundtick"
	"github.com/l4ci/rota/internal/roundwatch"
	"github.com/l4ci/rota/internal/verdict"
)

func TestTrainNudgeNamesTwoReviewedPRsOnOneBase(t *testing.T) {
	ns := trainNudges([]waitingPR{
		{PR: "https://example.test/o/r/pull/12", Base: "main", Reviewed: true},
		{PR: "#9", Base: "main", Reviewed: true},
		{PR: "#30", Base: "main", Reviewed: false},
		{PR: "#31", Base: "cycle", Reviewed: true},
	})
	if len(ns) != 1 {
		t.Fatalf("nudges = %+v, want one (main)", ns)
	}
	if got, want := ns[0].Command(), "rota worker train 9 12 --base main"; got != want {
		t.Errorf("command = %q, want %q", got, want)
	}
	if txt := ns[0].Text(); !strings.Contains(txt, "#9 #12") || !strings.Contains(txt, "rota worker train") {
		t.Errorf("text names neither PRs nor command: %q", txt)
	}
}

func TestTrainNudgeAbsentWithOneReviewedPR(t *testing.T) {
	for name, w := range map[string][]waitingPR{
		"one waiting":            {{PR: "#9", Base: "main", Reviewed: true}},
		"second unreviewed":      {{PR: "#9", Base: "main", Reviewed: true}, {PR: "#10", Base: "main"}},
		"two on different bases": {{PR: "#9", Base: "main", Reviewed: true}, {PR: "#10", Base: "cycle", Reviewed: true}},
		"none":                   nil,
	} {
		if ns := trainNudges(w); len(ns) != 0 {
			t.Errorf("%s: nudges = %+v, want none", name, ns)
		}
	}
}

// nudgeRepo is a repo with n done slots, each on its own branch at HEAD with a
// review-spec verdict recorded there, so every PR counts as reviewed.
func nudgeRepo(t *testing.T, n int) string {
	t.Helper()
	root := gitRepo(t)
	head, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.TrimSpace(string(head))
	var slots []string
	for i := 1; i <= n; i++ {
		branch := fmt.Sprintf("w%d/%d-x", i, i)
		if out, err := exec.Command("git", "-C", root, "branch", branch).CombinedOutput(); err != nil {
			t.Fatalf("git branch: %v %s", err, out)
		}
		rec := verdict.NewRecord(verdict.ReviewSpec, verdict.Pass, sha, verdict.Body{}, time.Now())
		if _, err := verdict.AddBranch(root, verdict.BranchKey("", branch), rec); err != nil {
			t.Fatal(err)
		}
		slots = append(slots, fmt.Sprintf(`{"name":"w%d","branch":%q,"state":"done","base":"feat/x","pr":"https://github.com/o/r/pull/%d"}`, i, branch, 10+i))
	}
	write(t, filepath.Join(root, ".rota", "workers.json"), `{"slots":[`+strings.Join(slots, ",")+`]}`)
	return root
}

// TestWatchResultNudgesAfterATickWithoutMergeItems: an autopilot tick that
// left no merge item still shows the train nudge when two reviewed PRs wait on
// one base, and shows none with one.
func TestWatchResultNudgesAfterATickWithoutMergeItems(t *testing.T) {
	for _, tc := range []struct {
		waiting int
		want    bool
	}{{2, true}, {1, false}} {
		root := nudgeRepo(t, tc.waiting)
		c := &Ctx{Deps: testDeps(), ctx: context.Background()}
		cd, err := rotastate.CommonDir(root)
		if err != nil {
			t.Fatal(err)
		}
		tick := roundtick.Result{}
		res := watchResult(c, root, cd, c.deps().LeaseEnv(), roundwatch.Result{Reason: roundwatch.ReasonChange}, &tick, "")
		got := strings.Contains(res.Text, "nudge\ttrain\t2 reviewed PRs wait on feat/x (#11 #12)\trota worker train 11 12 --base feat/x")
		if got != tc.want {
			t.Errorf("%d waiting: nudge present = %v, want %v\n%s", tc.waiting, got, tc.want, res.Text)
		}
		if _, has := res.Data.(*jsonx.Object).Get("trainNudge"); has != tc.want {
			t.Errorf("%d waiting: trainNudge key present = %v, want %v", tc.waiting, has, tc.want)
		}
	}
}

func TestTrainNudgeGroupsEmptyBaseWithDefault(t *testing.T) {
	w := fillDefaultBase([]waitingPR{
		{PR: "#9", Base: "", Reviewed: true},
		{PR: "#10", Base: "main", Reviewed: true},
	}, "main")
	ns := trainNudges(w)
	if len(ns) != 1 || ns[0].Base != "main" || len(ns[0].PRs) != 2 {
		t.Fatalf("nudges = %+v, want one train of two on main", ns)
	}
}
