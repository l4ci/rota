package cli

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// The round brief asks its Forge whether an issue's author may push (#899): a
// GitHub issue side must come through forgeAndIssues as a worker.IssueTrust, a
// file source's must not.
func TestForgeAndIssuesPassesTheTrustCheckThrough(t *testing.T) {
	for source, want := range map[string]bool{"github": true, "file": false} {
		root := gitRepo(t)
		write(t, filepath.Join(root, ".rota", "config.json"), `{"forge": {"provider": "github"}, "issues": {"source": "`+source+`"}}`)
		deps := testDeps()
		deps.TrackerOptions = []tracker.Option{tracker.WithExec(func(context.Context, string, string, []string, []byte) ([]byte, []byte, int, error) {
			return []byte("[]"), nil, 0, nil
		}, func(n string) (string, error) { return "/fake/" + n, nil })}
		f, err := deps.forgeAndIssues(context.Background(), config.Load(rotatree.Config(root)), root)
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		if _, ok := f.(worker.IssueTrust); ok != want {
			t.Errorf("%s source: IssueTrust = %v, want %v", source, ok, want)
		}
	}
}
