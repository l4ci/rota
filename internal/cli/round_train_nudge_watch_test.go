package cli

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/roundlease"
	"github.com/l4ci/rota/internal/roundtick"
	"github.com/l4ci/rota/internal/roundwatch"
	"github.com/l4ci/rota/internal/verdict"
)

// nudgeWatchRoot is a repo with two done slots, each with a PR on main and a
// branch at HEAD; reviewed records a PASS review at HEAD for both branches.
func nudgeWatchRoot(t *testing.T, reviewed bool) string {
	t.Helper()
	root := gitRepo(t)
	for _, b := range []string{"dana/1-a", "kit/2-b"} {
		gitIn(t, root, "branch", b)
	}
	write(t, filepath.Join(root, ".rota", "workers.json"), `{"slots":[
{"name":"dana","branch":"dana/1-a","pr":"https://example.test/o/r/pull/11","state":"done","base":"main"},
{"name":"kit","branch":"kit/2-b","pr":"https://example.test/o/r/pull/12","state":"done","base":"main"}]}`)
	if reviewed {
		out, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
		if err != nil {
			t.Fatal(err)
		}
		head := strings.TrimSpace(string(out))
		for _, b := range []string{"dana/1-a", "kit/2-b"} {
			if _, err := verdict.AddBranch(root, b, verdict.Record{Kind: verdict.ReviewSpec, Verdict: verdict.Pass, Sha: head}); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root
}

func nudgeWatch(t *testing.T, root string, tick *roundtick.Result) Result {
	t.Helper()
	c := &Ctx{Deps: testDeps(), ctx: context.Background()}
	return watchResult(c, root, t.TempDir(), roundlease.Env{}, roundwatch.Result{Reason: roundwatch.ReasonHeartbeat}, tick, "")
}

func TestWatchResultCarriesTrainNudgeForReviewedPRs(t *testing.T) {
	res := nudgeWatch(t, nudgeWatchRoot(t, true), nil)
	rows, ok := res.Data.(*jsonx.Object).Get("trainNudge")
	if !ok || len(rows.([]any)) != 1 {
		t.Fatalf("trainNudge = %v (present %v), want one row", rows, ok)
	}
	if !strings.Contains(res.Text, "nudge\ttrain\t2 reviewed PRs wait on main (#11 #12)\trota worker train 11 12 --base main") {
		t.Errorf("text lacks the nudge line:\n%s", res.Text)
	}
}

func TestWatchResultOmitsTrainNudge(t *testing.T) {
	for name, tc := range map[string]struct {
		reviewed bool
		tick     *roundtick.Result
	}{
		"unreviewed PRs":             {false, nil},
		"autopilot trains by itself": {true, &roundtick.Result{}},
	} {
		res := nudgeWatch(t, nudgeWatchRoot(t, tc.reviewed), tc.tick)
		if _, ok := res.Data.(*jsonx.Object).Get("trainNudge"); ok {
			t.Errorf("%s: data carries trainNudge", name)
		}
		if strings.Contains(res.Text, "nudge\ttrain") {
			t.Errorf("%s: text carries a nudge:\n%s", name, res.Text)
		}
	}
}

func TestWatchResultNudgesWhenTickLeavesMergeForPerson(t *testing.T) {
	tick := &roundtick.Result{NeedsYou: []roundtick.Item{{Kind: "merge"}}}
	res := nudgeWatch(t, nudgeWatchRoot(t, true), tick)
	if _, ok := res.Data.(*jsonx.Object).Get("trainNudge"); !ok {
		t.Errorf("tick needing a merge: no trainNudge in %v", res.Data)
	}
}
