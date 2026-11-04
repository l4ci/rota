package cli

import (
	"context"
	"testing"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/worker"
)

// fakeHeads answers rev-parse from a map of ref to SHA; unknown refs exit 1.
func fakeHeads(heads map[string]string) git.Runner {
	return func(_ context.Context, _ string, args ...string) (git.Result, error) {
		if len(args) >= 1 && args[0] == "rev-parse" {
			if sha, ok := heads[args[len(args)-1]]; ok {
				return git.Result{Stdout: sha + "\n"}, nil
			}
		}
		return git.Result{ExitCode: 1}, nil
	}
}

func TestPlanNudgesCachesByHeads(t *testing.T) {
	heads := map[string]string{"main": "b1", "origin/main": "b1", "ben/9": "h9", "dana/10": "h10"}
	c := &Ctx{Deps: &Deps{Git: fakeHeads(heads)}}
	calls := 0
	c.Deps.TrainPlan = func(*Ctx, string, string, []string) (*worker.TrainPlan, error) {
		calls++
		return &worker.TrainPlan{
			Landable:  []string{"9"},
			Conflicts: []worker.TrainConflict{{A: "9", B: "10"}},
			Blocked:   []worker.TrainBlocked{{Target: "10", By: []string{"9"}}},
		}, nil
	}
	root := t.TempDir()
	eval := func() trainNudge {
		ns := trainNudges([]waitingPR{
			{PR: "#9", Branch: "ben/9", Base: "main", Reviewed: true},
			{PR: "#10", Branch: "dana/10", Base: "main", Reviewed: true},
		})
		return planNudges(c, root, ns)[0]
	}
	first := eval()
	if len(first.Conflicts) != 1 || calls != 1 {
		t.Fatalf("first: conflicts=%d calls=%d", len(first.Conflicts), calls)
	}
	if again := eval(); calls != 1 || again.Text() != first.Text() {
		t.Errorf("unchanged heads re-planned: calls=%d", calls)
	}
	heads["dana/10"] = "h10b"
	eval()
	if calls != 2 {
		t.Errorf("moved head did not re-plan: calls=%d", calls)
	}
}
