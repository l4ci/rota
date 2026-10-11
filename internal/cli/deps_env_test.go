package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
	if env.Worker.Accounts != nil || env.AccountPicking {
		t.Error("RoundEnv: account picking must stay off")
	}
	if env.Worker.Git == nil {
		t.Fatal("RoundEnv: Worker.Git is nil")
	}
	env.Worker.Git(context.Background(), ".", "status")
	if calls == 0 {
		t.Error("RoundEnv: Worker.Git did not reach the faked Deps.Git")
	}
	c := &Ctx{Deps: d}
	if got := accountedRoundEnv(c, context.Background(), root); !got.AccountPicking || got.Worker.Accounts == nil {
		t.Error("accountedRoundEnv: account picking is not on")
	}
	// The verbs that build RoundEnv plus workerEnvCtx (adopt, review relay,
	// winddown) get the meter reader but never pick.
	plain := d.RoundEnv(context.Background(), root)
	plain.Worker = workerEnvCtx(c, context.Background())
	if plain.AccountPicking {
		t.Error("RoundEnv + workerEnvCtx: account picking must stay off")
	}
}

// Account picking is chosen where the env is built (accountedRoundEnv): no verb
// sets the flag or Worker.Accounts on a round env itself, and the accounting
// verbs (assign, tick, architecture, return, status's quota read) go through
// the constructor.
func TestAccountPickingChosenOnlyInTheConstructor(t *testing.T) {
	files, err := filepath.Glob("round_*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		for _, bad := range []string{".Worker.Accounts =", "AccountPicking ="} {
			if f != "round_status.go" && strings.Contains(src, bad) {
				t.Errorf("%s sets %q on an env after it was built; use accountedRoundEnv", f, bad)
			}
		}
		want := map[string]bool{"round_assign.go": true, "round_tick.go": true, "round_architecture.go": true, "round_return.go": true, "round_status.go": true}
		if want[f] && !strings.Contains(src, "accountedRoundEnv(") {
			t.Errorf("%s must build its env with accountedRoundEnv", f)
		}
		if !want[f] && strings.Contains(src, "accountedRoundEnv(") && f != "round_status.go" {
			t.Errorf("%s uses accountedRoundEnv but is not an accounting verb", f)
		}
	}
}
