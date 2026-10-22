package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/roundlease"
)

// keepalive status finds the lease through Deps.Git: a project that is no git
// repository, with a fake git naming its common dir, still reads that lease.
func TestKeepaliveStatusResolvesCommonDirThroughFakeGit(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	if err := os.MkdirAll(filepath.Join(dir, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	cd := filepath.Join(dir, "fake-common")
	if err := os.MkdirAll(cd, 0o755); err != nil {
		t.Fatal(err)
	}
	env := roundlease.DefaultEnv()
	if _, _, _, err := env.Acquire(cd, dir, roundlease.Holder{PID: 1, Start: mustStart(env, 1)}, 7); err != nil {
		t.Fatal(err)
	}
	pinDir(t, dir, func(d *Deps) {
		d.Git = func(_ context.Context, _ string, args ...string) (git.Result, error) {
			if len(args) > 0 && args[0] == "rev-parse" {
				return git.Result{Stdout: cd + "\n"}, nil
			}
			return git.Result{ExitCode: 1}, nil
		}
	})
	code, out, _ := rotaIn(t, dir, "keepalive", "status", "--json")
	l, _ := data(t, out)["lease"].(map[string]any)
	if code != 0 || l["state"] != "live" || l["round"] != float64(7) {
		t.Fatalf("exit %d lease %v out %s", code, l, out)
	}
}
