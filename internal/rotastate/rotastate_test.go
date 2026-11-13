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

func TestTempRootViaResolvesMainCheckoutAndIgnoresItself(t *testing.T) {
	repo := gittest.NewRepo(t, "main")
	real, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(repo, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(t.TempDir(), "wt")
	gittest.Run(t, repo, "worktree", "add", "-q", wt, "-b", "other")
	want := filepath.Join(real, ".rota", "tmp")

	for name, dir := range map[string]string{"main": repo, "linked worktree": wt} {
		got, ok, err := TempRootVia(context.Background(), git.Exec, dir)
		if err != nil || !ok || got != want {
			t.Fatalf("%s: TempRootVia = %q, %v, %v; want %q", name, got, ok, err, want)
		}
	}
	fi, err := os.Stat(want)
	if err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
		t.Fatalf("temp root: %v, %v; want a 0700 directory", fi, err)
	}
	// The root ignores itself, so a project whose .gitignore predates it still
	// keeps its contents out of git status (and so out of classify and overlap).
	if err := os.WriteFile(filepath.Join(want, "scratch.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out := gittest.Run(t, repo, "status", "--porcelain", "--untracked-files=all"); out != "" {
		t.Errorf("git status shows the temp root:\n%s", out)
	}
	if got, ok := TempRoot(filepath.Join(real, ".git")); !ok || got != want {
		t.Errorf("TempRoot = %q, %v; want %q without creating", got, ok, want)
	}
}

func TestTempRootViaWithoutProjectFallsBack(t *testing.T) {
	plain := gittest.NewRepo(t, "main")
	if got, ok, err := TempRootVia(context.Background(), git.Exec, plain); err != nil || ok {
		t.Errorf("repo without .rota: TempRootVia = %q, %v, %v; want no root", got, ok, err)
	}
	if _, err := os.Stat(filepath.Join(plain, ".rota")); !os.IsNotExist(err) {
		t.Errorf("repo without .rota: created %v", err)
	}
	if got, ok, err := TempRootVia(context.Background(), git.Exec, t.TempDir()); err != nil || ok {
		t.Errorf("outside a repository: TempRootVia = %q, %v, %v; want no root, no error", got, ok, err)
	}
}
