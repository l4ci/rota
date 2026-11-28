package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/repos"
)

// A linked worktree outside the sub-repo's folder still belongs to it: the
// issues verbs and label sync run the forge CLI there, not at the umbrella.
func TestIssuesScopeLinkedWorktree(t *testing.T) {
	root := repos.Realpath(trackerProject(t, `{"forge":{"provider":"github"},"issues":{"source":"github"}}`))
	api := filepath.Join(root, "api")
	gittest.Init(t, api, "main")
	gittest.Run(t, api, "remote", "add", "origin", "https://github.com/o/api.git")
	gittest.Run(t, api, "commit", "-q", "--allow-empty", "-m", "init")
	linked := filepath.Join(root, ".claude", "worktrees", "api", "feat")
	gittest.Run(t, api, "worktree", "add", "-q", "-b", "feat", linked)
	if err := os.WriteFile(filepath.Join(root, ".rota", "repos.json"), []byte(`{"repos":[{"name":"api","path":"api"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	nested := filepath.Join(api, "vendored")
	gittest.Init(t, nested, "main")

	for _, tc := range []struct{ name, cwd, want string }{
		{"linked worktree", linked, api},
		{"sub-repo", api, api},
		{"umbrella root", root, root},
		// a repository of its own inside a sub-repo is not that sub-repo
		{"nested repo", nested, root},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps := withTracker(t, syncFixture())
			deps.Getwd = func() (string, error) { return tc.cwd, nil }
			var got string
			inner := deps.NewTracker
			deps.NewTracker = func(ctx context.Context, dir string, cfg any) (backlog.Tracker, error) {
				got = dir
				return inner(ctx, dir, cfg)
			}
			code, _, stderr := rotaRunWith(t, deps, "--json", "labels", "sync")
			if code != 0 || got != tc.want {
				t.Errorf("labels sync: exit %d, dir %q, want %q: %s", code, got, tc.want, stderr)
			}
		})
	}
}
