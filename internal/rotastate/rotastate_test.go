package rotastate

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/gittest"
)

func TestCommonDirResolvesSymlinksAndLinkedWorktrees(t *testing.T) {
	repo := gittest.NewRepo(t, "main")
	real, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(real, ".git")

	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(t.TempDir(), "wt")
	gittest.Run(t, repo, "worktree", "add", "-q", wt, "-b", "other")

	for name, dir := range map[string]string{"main": repo, "symlinked": link, "linked worktree": wt} {
		got, err := CommonDirVia(context.Background(), git.Exec, dir)
		if err != nil || got != want {
			t.Errorf("%s: CommonDirVia = %q, %v; want %q", name, got, err, want)
		}
	}
}

func TestCommonDirOutsideRepo(t *testing.T) {
	if _, err := CommonDirVia(context.Background(), git.Exec, t.TempDir()); err == nil {
		t.Fatal("want an error outside a repository")
	}
}

func TestLayout(t *testing.T) {
	cd := "/r/.git"
	for got, want := range map[string]string{
		Dir(cd):            "/r/.git/rota",
		File(cd, "x.json"): "/r/.git/rota/x.json",
		SessionDir(cd):     "/r/.git/rota/session",
		CodexDir(cd):       "/r/.git/rota/codex",
	} {
		if got != want {
			t.Errorf("got %s, want %s", got, want)
		}
	}
}

func TestMainCheckoutViaNormalBareAndLinkedWorktree(t *testing.T) {
	repo := gittest.NewRepo(t, "main")
	real, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(t.TempDir(), "wt")
	gittest.Run(t, repo, "worktree", "add", "-q", wt, "-b", "other")
	bare := filepath.Join(t.TempDir(), "r.git")
	gittest.Run(t, repo, "clone", "-q", "--bare", repo, bare)

	for name, dir := range map[string]string{"normal": repo, "symlinked": link, "linked worktree": wt} {
		got, ok, err := MainCheckoutVia(context.Background(), git.Exec, dir)
		if err != nil || !ok || got != real {
			t.Errorf("%s: MainCheckoutVia = %q, %v, %v; want %q", name, got, ok, err, real)
		}
	}
	if got, ok, err := MainCheckoutVia(context.Background(), git.Exec, bare); err != nil || ok {
		t.Errorf("bare: MainCheckoutVia = %q, %v, %v; want no main checkout, no error", got, ok, err)
	}
	if _, _, err := MainCheckoutVia(context.Background(), git.Exec, t.TempDir()); err == nil {
		t.Error("outside a repository: want an error")
	}
}
