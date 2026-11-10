package round

import (
	"testing"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/worker"
)

// The holder pid reaches every verb's lease check through Env.HolderPID alone.
// Pick, Return, Reclaim, WindDown, Assign and Transfer prove it in their own
// refusal tests (a foreign pid is refused); Start has no refusal, so its test
// reads the pid back off the lease it takes.

func TestStartTakesTheHolderPIDFromTheEnv(t *testing.T) {
	root := newRepo(t, nil)
	e := Env{Git: git.Exec, Base: "main", Worker: wk(worker.Env{Getenv: noEnv, Config: diskConfig}), Lease: fakeLease("h", 100, 200), HolderPID: 100}
	st, err := e.Start(bg, root, StartOpts{Scope: roundcfg.ScopeMilestone, Settings: startOpts(roundcfg.ScopeMilestone).Settings, DefaultNum: 2, Dispatch: "tmux"})
	if err != nil {
		t.Fatal(err)
	}
	if st.Lease.PID != 100 {
		t.Fatalf("lease bound to pid %d, want the env's 100", st.Lease.PID)
	}
}
