package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/rotastatetest"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundtick"
	"github.com/l4ci/rota/internal/roundwatch"
)

func TestWatchBranchNoPRReportsStateChanges(t *testing.T) {
	root := gitRepo(t)
	cd, err := rotastatetest.CommonDir(root)
	if err != nil {
		t.Fatal(err)
	}
	c := &Ctx{Deps: testDeps(), ctx: context.Background()}
	for i, step := range []struct {
		state string
		want  int
	}{
		{"idle", 1}, {"idle", 0}, {"done", 1}, {"done", 0},
		{"busy", 0}, {"idle", 1},
	} {
		e := roundtick.Env{
			Slots: func() []roundtick.Slot {
				return []roundtick.Slot{{Name: "kit", State: step.state, Issue: "2179"}}
			},
			Reconcile: func(context.Context) ([]string, []string, error) {
				if step.state == "busy" {
					return nil, nil, nil
				}
				// Keep another drift kind present to guard the ticket's scope.
				return nil, []string{round.BranchNoPR + " kit", round.LabelMissing + " kit"}, nil
			},
		}
		roundtick.LoadState(cd, 40).Apply(&e)
		tick, err := roundtick.Run(context.Background(), e)
		if err != nil {
			t.Fatal(err)
		}
		if err := roundtick.SaveState(cd, 40, tick); err != nil {
			t.Fatal(err)
		}
		res := watchResult(c, root, cd, c.deps().LeaseEnv(), roundwatch.Result{Reason: roundwatch.ReasonHeartbeat}, &tick, "")
		b, err := jsonx.Marshal(res.Data)
		if err != nil {
			t.Fatal(err)
		}
		var data struct {
			Autopilot struct{ NeedsYou []roundtick.Item }
		}
		if err := json.Unmarshal(b, &data); err != nil {
			t.Fatal(err)
		}
		jsonCount := 0
		for _, item := range data.Autopilot.NeedsYou {
			if item.Target == "branch-no-pr kit" {
				jsonCount++
			}
		}
		textCount := strings.Count(res.Text, "branch-no-pr kit")
		t.Logf("wake=%d state=%s branch-no-pr text=%d JSON=%d", i+1, step.state, textCount, jsonCount)
		if textCount != step.want || jsonCount != step.want {
			t.Errorf("wake %d (%s): branch-no-pr text=%d JSON=%d, want %d", i+1, step.state, textCount, jsonCount, step.want)
		}
		if step.state != "busy" && !strings.Contains(res.Text, "label-missing kit") {
			t.Errorf("other drift disappeared: %q", res.Text)
		}
		if !strings.Contains(res.Text, "heartbeat\t") {
			t.Errorf("heartbeat disappeared: %q", res.Text)
		}
	}
}
