package rotastate

import (
	"os"
	"path/filepath"
	"testing"

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
		got, err := CommonDir(dir)
		if err != nil || got != want {
			t.Errorf("%s: CommonDir = %q, %v; want %q", name, got, err, want)
		}
	}
	if main, ok := MainCheckout(want); !ok || main != real {
		t.Errorf("MainCheckout = %q, %v; want %q", main, ok, real)
	}
}

func TestCommonDirOutsideRepo(t *testing.T) {
	if _, err := CommonDir(t.TempDir()); err == nil {
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
	if _, ok := MainCheckout("/r.git"); ok {
		t.Error("a bare repository has no main checkout")
	}
}
