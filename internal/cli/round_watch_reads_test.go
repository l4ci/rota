package cli

import (
	"context"

	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundwatch"
	"github.com/l4ci/rota/internal/tracker"
)

// TestWatchForgeSeesAChangeBetweenPasses: the read cache lives for the whole
// process, and a watch lives for hours, so each pass must start from an empty
// cache or a merge is never seen after the first tick.
func TestWatchForgeSeesAChangeBetweenPasses(t *testing.T) {
	root := gitRepo(t)
	write(t, filepath.Join(root, ".rota", "config.json"), `{"backlog":{"backend":"issues"},"issues":{"provider":"github"}}`)
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
		f, err := deps.forge(ctx, config.Load(rotatree.Config(root)), "", root)
		if err != nil {
			t.Fatal(err)
		}
		return round.Env{Git: git.Exec, Base: "feat/x", HostName: "solo", Forge: f}
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
