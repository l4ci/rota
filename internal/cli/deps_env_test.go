package cli

import (
	"context"
	"testing"

	"github.com/l4ci/rota/internal/git"
)

// TestFakeGitReachesWorkerAndRoundEnv pins the one Git source: a faked
// Deps.Git is the runner of the worker env and of the round env's worker.
func TestFakeGitReachesWorkerAndRoundEnv(t *testing.T) {
	d := testDeps()
	var calls int
	d.Git = func(context.Context, string, ...string) (git.Result, error) {
		calls++
		return git.Result{}, nil
	}
	envs := map[string]func(context.Context, string, ...string) (git.Result, error){
		"workerEnv":    d.workerEnv().Git,
		"baseRoundEnv": d.baseRoundEnv().Git,
		"round worker": d.baseRoundEnv().Worker.Git,
	}
	for name, run := range envs {
		before := calls
		if run == nil {
			t.Errorf("%s: Git is nil", name)
			continue
		}
		run(context.Background(), ".", "status")
		if calls != before+1 {
			t.Errorf("%s: did not reach the faked Deps.Git", name)
		}
	}
}
