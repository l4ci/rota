package round

import (
	"fmt"
	"github.com/l4ci/rota/internal/cfgdir"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/testiso"
	"github.com/l4ci/rota/internal/worker"
	"os"
	"testing"
)

// TestMain puts tripwire codex, herdr and tmux first on PATH. A round runs
// inside herdr and a codex worker's preflight looks codex up on PATH: any test
// that reaches a real binary fails the whole run instead of running it.
func TestMain(m *testing.M) {
	tw, err := testiso.NewTripwire("round-tripwire", "codex", "herdr", "tmux")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	gittest.SetIdentity() // the code under test commits and merges as the process
	code := m.Run()
	if hit := tw.Check(); hit != "" {
		fmt.Fprintf(os.Stderr, "FAIL: tests exec'd a host or codex binary from PATH:\n%s", hit)
		code = 1
	}
	tw.Close()
	os.Exit(code)
}

// noEnv is an empty process environment for Env.Worker.Getenv.
func noEnv(string) string { return "" }

// diskConfig reads the .rota/config.json a test wrote, as cli's Deps.Config does.
func diskConfig(root string) any { return config.Load(cfgdir.OSEnv(), root) }

// wk adapts w as an Env.Worker, defaulting its Git to the real runner the way
// the tests' Env literals do.
func wk(w worker.Env) Worker {
	if w.Git == nil {
		w.Git = git.Exec
	}
	return FromWorkerEnv(w)
}
