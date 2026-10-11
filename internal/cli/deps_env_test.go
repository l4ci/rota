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

// TestRoundEnvAccountsAndGit pins what RoundEnv hands round verbs: Accounts nil
// (quota and account picking are opt-in, so round start keeps its path), and
// the faked Deps.Git reachable through Worker.Git; accountedRoundEnv opts in.
func TestRoundEnvAccountsAndGit(t *testing.T) {
	d := testDeps()
	var calls int
	d.Git = func(context.Context, string, ...string) (git.Result, error) {
		calls++
		return git.Result{}, nil
	}
	root := t.TempDir()
	env := d.RoundEnv(context.Background(), root)
	if env.Worker.Accounts != nil {
		t.Error("RoundEnv: Worker.Accounts must stay nil")
	}
	if env.Worker.Git == nil {
		t.Fatal("RoundEnv: Worker.Git is nil")
	}
	env.Worker.Git(context.Background(), ".", "status")
	if calls == 0 {
		t.Error("RoundEnv: Worker.Git did not reach the faked Deps.Git")
	}
	c := &Ctx{Deps: d}
	if got := accountedRoundEnv(c, context.Background(), root); got.Worker.Accounts == nil {
		t.Error("accountedRoundEnv: Worker.Accounts is nil")
	}
}
