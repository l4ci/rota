package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/roundlease"
	"github.com/l4ci/rota/internal/roundwatch"
)

// assignNudgeRepo is a file-backend repo whose only slot is done with a PR,
// with a ready candidate in the backlog when withCandidate is set.
func assignNudgeRepo(t *testing.T, withCandidate bool) string {
	t.Helper()
	dir := verdictRepo(t)
	for f, c := range map[string]string{"BACKLOG.md": "# TODO\n\n## Tasks\n\n## Completed\n", "counters.json": "{}\n"} {
		if err := os.WriteFile(filepath.Join(dir, ".rota", f), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if withCandidate {
		body := writeBody(t, t.TempDir(), `{"findings":[{"severity":"minor","title":"rename the helper"}]}`)
		if code, _, errOut := rotaIn(t, dir, "verdict", "add", "--kind", "review-quality", "--verdict", "FAIL", "--body-file", body, "--file-deferred", "--pr", "#77"); code != 0 {
			t.Fatalf("file candidate: %d %s", code, errOut)
		}
	}
	write(t, filepath.Join(dir, ".rota", "workers.json"),
		`{"slots":[{"name":"w1","branch":"w1/1-x","state":"done","pr":"https://github.com/o/r/pull/11"},{"name":"w2","branch":"w2/2-y","state":"busy"}]}`)
	return dir
}

func watchedSlot(t *testing.T, dir, slot string) Result {
	t.Helper()
	c := &Ctx{Deps: testDeps(), ctx: context.Background()}
	cd, err := rotastate.CommonDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	res := roundwatch.Result{Reason: roundwatch.ReasonChange, Slot: &roundwatch.SlotNews{Slot: slot, State: "done"}}
	return watchResult(c, dir, cd, roundlease.DefaultEnv(), res, nil, "")
}

// AC-1/AC-2 (#1060): a done slot with a PR returns while a candidate is ready:
// the watch names the candidate and the assign command; with none, nothing extra.
func TestWatchNudgesToAssignBeforeGating(t *testing.T) {
	for _, tc := range []struct {
		name          string
		withCandidate bool
		slot          string
		want          bool
	}{
		{"candidate left", true, "w1", true},
		{"no candidates", false, "w1", false},
		{"slot not done", true, "w2", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := watchedSlot(t, assignNudgeRepo(t, tc.withCandidate), tc.slot)
			got := strings.Contains(res.Text, "assign before you gate") && strings.Contains(res.Text, "rota round assign ")
			if got != tc.want {
				t.Errorf("nudge present = %v, want %v\n%s", got, tc.want, res.Text)
			}
			if _, has := res.Data.(*jsonx.Object).Get("assignNudge"); has != tc.want {
				t.Errorf("assignNudge key present = %v, want %v", has, tc.want)
			}
		})
	}
}
