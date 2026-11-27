package repos_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/knowledge"
	"github.com/l4ci/rota/internal/repos"
	"github.com/l4ci/rota/internal/rotatree"
)

// Exercise the shared membership seam and its three error policies together.
func TestLookupCheckouts(t *testing.T) {
	root := repos.Realpath(t.TempDir())
	web := filepath.Join(root, "web")
	if err := os.MkdirAll(filepath.Join(root, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	gittest.Init(t, web, "main")
	gittest.Run(t, web, "commit", "-q", "--allow-empty", "-m", "init")
	deep := filepath.Join(web, "src", "deep")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(root, "linked")
	gittest.Run(t, web, "worktree", "add", "-q", "-b", "feature", linked)
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(web, alias); err != nil {
		t.Fatal(err)
	}
	// The first name wins even when a later name registers the same checkout.
	// The registry path itself is also symlinked.
	if err := os.WriteFile(filepath.Join(root, ".rota", "repos.json"), []byte(`{"repos":[{"name":"web","path":"alias"},{"name":"duplicate","path":"web"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	list := repos.Load(root)
	for _, tc := range []struct{ name, cwd string }{
		{"main checkout", web},
		{"nested directory", deep},
		{"linked worktree", linked},
		{"symlinked path", alias},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := repos.Lookup(git.Exec, tc.cwd, list)
			if err != nil || r.Name != "web" || r.Path != web {
				t.Fatalf("Lookup = %+v, %v; want web at %s", r, err, web)
			}
			if got, err := repos.Which(git.Exec, tc.cwd); err != nil || got != r {
				t.Errorf("Which = %+v, %v; want %+v", got, err, r)
			}
			if got := repos.VerdictRepo(git.Exec, root, tc.cwd, ""); got != "web" {
				t.Errorf("verdict key = %q; want web", got)
			}
			if got := backlog.CwdSubRepo(git.Exec, tc.cwd, list); got != "web" {
				t.Errorf("backlog sub-repo = %q; want web", got)
			}
			if got := knowledge.DefaultScope(git.Exec, tc.cwd, list); got != "web" {
				t.Errorf("knowledge scope = %q; want web", got)
			}
		})
	}
}

func TestLookupRunnerAndFallbacks(t *testing.T) {
	root := repos.Realpath(t.TempDir())
	if err := os.MkdirAll(filepath.Join(root, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".rota", "repos.json"), []byte(`{"repos":[{"name":"web","path":"web"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	list := repos.Load(root)
	for _, tc := range []struct {
		name                string
		result              git.Result
		err, wantErr        error
		wantName, wantScope string
	}{
		{"injected checkout", git.Result{Stdout: "web/.git"}, nil, nil, "web", "web"},
		{"unregistered", git.Result{Stdout: "other/.git"}, nil, repos.ErrNotRegistered, "", knowledge.Umbrella},
		{"bare repo", git.Result{Stdout: "bare.git"}, nil, repos.ErrNotGit, "", knowledge.Umbrella},
		{"not a repo", git.Result{ExitCode: 128}, nil, repos.ErrNotGit, "", knowledge.Umbrella},
		{"missing git", git.Result{}, git.ErrNoGit, repos.ErrGitMissing, "", knowledge.Umbrella},
		{"runner error", git.Result{}, errors.New("offline"), repos.ErrNotGit, "", knowledge.Umbrella},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := func(_ context.Context, cwd string, _ ...string) (git.Result, error) {
				if cwd != root {
					t.Fatalf("runner cwd = %q; want %q", cwd, root)
				}
				return tc.result, tc.err
			}
			r, err := repos.Lookup(run, root, list)
			if !errors.Is(err, tc.wantErr) || r.Name != tc.wantName {
				t.Errorf("Lookup = %+v, %v; want %q, %v", r, err, tc.wantName, tc.wantErr)
			}
			if r, err := repos.Which(run, root); !errors.Is(err, tc.wantErr) || r.Name != tc.wantName {
				t.Errorf("Which = %+v, %v; want %q, %v", r, err, tc.wantName, tc.wantErr)
			}
			if got := repos.VerdictRepo(run, root, root, ""); got != tc.wantName {
				t.Errorf("verdict key = %q; want %q", got, tc.wantName)
			}
			if got := backlog.CwdSubRepo(run, root, list); got != tc.wantName {
				t.Errorf("backlog sub-repo = %q; want %q", got, tc.wantName)
			}
			if got := knowledge.DefaultScope(run, root, list); got != tc.wantScope {
				t.Errorf("knowledge scope = %q; want %q", got, tc.wantScope)
			}
		})
	}
}

// Root discovery must not mask the git failure that Which reported first.
func TestWhichRootErrorPrecedence(t *testing.T) {
	cwd := t.TempDir()
	for _, tc := range []struct {
		name         string
		result       git.Result
		err, wantErr error
	}{
		{"missing git", git.Result{}, git.ErrNoGit, repos.ErrGitMissing},
		{"not a repo", git.Result{ExitCode: 128}, nil, repos.ErrNotGit},
		{"no umbrella", git.Result{Stdout: ".git"}, nil, rotatree.ErrNoRoot},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := func(context.Context, string, ...string) (git.Result, error) { return tc.result, tc.err }
			if _, err := repos.Which(run, cwd); !errors.Is(err, tc.wantErr) {
				t.Fatalf("Which = %v; want %v", err, tc.wantErr)
			}
		})
	}
}
