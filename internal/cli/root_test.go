package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/rotatree"
)

func TestCtxRootKeepsNearestOnMaskedLayout(t *testing.T) {
	top, _ := filepath.EvalSymlinks(t.TempDir())
	sub := filepath.Join(top, "svc")
	writeFile(t, filepath.Join(top, ".rota", "repos.json"), `{"repos":[{"name":"svc","path":"svc"}]}`)
	writeFile(t, filepath.Join(sub, ".rota", "x"), "")
	d := testDeps()
	d.Getwd = func() (string, error) { return sub, nil }
	got, err := (&Ctx{Deps: d}).Root()
	if err != nil || got != sub {
		t.Fatalf("Ctx.Root = %q, %v; want nearest %q (non-masking)", got, err, sub)
	}
}

func TestCtxCommonDirHelpers(t *testing.T) {
	d := testDeps()
	repo, _ := filepath.EvalSymlinks(t.TempDir())
	common := filepath.Join(repo, ".git")
	d.Git = func(ctx context.Context, dir string, args ...string) (git.Result, error) {
		return git.Result{Stdout: common + "\n"}, nil
	}
	c := &Ctx{Deps: d}
	if cd, err := c.CommonDir(repo); err != nil || cd != common {
		t.Fatalf("CommonDir = %q, %v; want %q", cd, err, common)
	}
	if main, ok := c.MainCheckout(repo); !ok || main != repo {
		t.Fatalf("MainCheckout = %q, %v; want %q", main, ok, repo)
	}

	d.Git = func(ctx context.Context, dir string, args ...string) (git.Result, error) {
		return git.Result{ExitCode: 128}, nil
	}
	if _, err := c.CommonDir(repo); !errors.Is(err, rotastate.ErrNotRepo) {
		t.Fatalf("not a repo: err = %v, want ErrNotRepo", err)
	}
	if _, ok := c.CommonDirOK(repo); ok {
		t.Fatal("CommonDirOK ok outside a repo")
	}
	if _, ok := c.MainCheckout(repo); ok {
		t.Fatal("MainCheckout ok outside a repo")
	}
}

func TestFindRootMaskingAndMissing(t *testing.T) {
	top, _ := filepath.EvalSymlinks(t.TempDir())
	sub := filepath.Join(top, "svc")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, masking := range []bool{false, true} {
		if _, err := findRoot(sub, masking); !errors.Is(err, rotatree.ErrNoRoot) {
			t.Fatalf("masking=%v, no .rota/: err = %v, want ErrNoRoot", masking, err)
		}
	}

	writeFile(t, filepath.Join(top, ".rota", "repos.json"), `{"repos":[{"name":"svc","path":"svc"}]}`)
	writeFile(t, filepath.Join(sub, ".rota", "x"), "")

	// Non-masking (Ctx.Root semantics): the stray nearest .rota/ wins.
	if got, err := findRoot(sub, false); err != nil || got != sub {
		t.Fatalf("masking off: findRoot = %q, %v; want %q", got, err, sub)
	}
	// Masking on: the stray one is reported as masked, not missing.
	_, err := findRoot(sub, true)
	if !errors.Is(err, rotatree.ErrMasked) {
		t.Fatalf("masking on: err = %v, want ErrMasked", err)
	}
	masked, missing := rootError(err), rootError(rotatree.ErrNoRoot)
	if !strings.Contains(masked.Message, "masks the umbrella") || strings.Contains(missing.Message, "masks") {
		t.Fatalf("messages not distinct: masked=%q missing=%q", masked.Message, missing.Message)
	}
	if !strings.Contains(missing.Message, "no .rota/ directory") {
		t.Fatalf("missing message = %q", missing.Message)
	}
}
