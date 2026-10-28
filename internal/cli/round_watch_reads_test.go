package cli

import (
	"context"
	"github.com/l4ci/rota/internal/projects"
	"os"

	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundwatch"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// TestWatchForgeSeesAChangeBetweenPasses: the read cache lives for the whole
// process, and a watch lives for hours, so each pass must start from an empty
// cache or a merge is never seen after the first tick.
func TestWatchForgeSeesAChangeBetweenPasses(t *testing.T) {
	root := gitRepo(t)
	write(t, filepath.Join(root, ".rota", "config.json"), `{"forge": {"provider": "github"}, "issues": {"source": "github"}}`)
	write(t, filepath.Join(root, ".rota", "workers.json"), `{"slots":[{"name":"dana","branch":"dana/58-x","pr":"https://github.com/o/r/pull/42"}]}`)
	state := "OPEN"
	deps := testDeps()
	deps.TrackerOptions = []tracker.Option{tracker.WithExec(func(_ context.Context, _, name string, args []string, _ []byte) ([]byte, []byte, int, error) {
		if len(args) > 1 && args[0] == "pr" && args[1] == "view" {
			return []byte(`{"state":"` + state + `"}`), nil, 0, nil
		}
		return []byte("[]"), nil, 0, nil
	}, func(n string) (string, error) { return "/fake/" + n, nil })}
	deps.RoundEnv = func(ctx context.Context, root string) round.Env {
		f, err := deps.forgeAndIssues(ctx, config.Load(projects.OSEnv(), rotatree.Config(root)), root)
		if err != nil {
			t.Fatal(err)
		}
		return round.Env{Worker: round.FromWorkerEnv(worker.Env{}), Git: git.Exec, Base: "feat/x", HostName: "solo", Forge: f}
	}
	c := &Ctx{Deps: deps, ctx: context.Background()}
	pass := func() string { return watchForge(context.Background(), c, root)[roundwatch.PRStateKey("dana")] }
	if got := pass(); !strings.EqualFold(got, "open") {
		t.Fatalf("first pass: PR state %q", got)
	}
	state = "MERGED"
	if got := pass(); !strings.EqualFold(got, "merged") {
		t.Fatalf("second pass still sees %q after the PR merged", got)
	}
}

// A registry write that fails is warned about once, not dropped silently.
func TestMarkMergedDoneReportsAFailedWrite(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	root := gitRepo(t)
	write(t, filepath.Join(root, ".rota", "workers.json"), `{"slots":[{"name":"dana","state":"done","pr":"https://github.com/o/r/pull/42"}]}`)
	if err := markMergedDone(root, map[string]bool{"dana": true}); err != nil {
		t.Fatalf("a writable registry failed: %v", err)
	}
	if !worker.LoadRegistryTolerant(root).Slot("dana").PRMerged() {
		t.Fatal("merged done slot not marked")
	}
	write(t, filepath.Join(root, ".rota", "workers.json"), `{"slots":[{"name":"dana","state":"done","pr":"https://github.com/o/r/pull/42"}]}`)
	dir := filepath.Join(root, ".rota")
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755)
	if err := markMergedDone(root, map[string]bool{"dana": true}); err == nil {
		t.Fatal("a failed write returned nil")
	}
}
