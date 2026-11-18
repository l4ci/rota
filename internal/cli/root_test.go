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

// A repos.json entry without a name still registers its tree: the stray
// .rota/ inside it masks the umbrella through findRoot, as through repo which.
func TestFindRootMasksOnNamelessAbsoluteEntry(t *testing.T) {
	top, _ := filepath.EvalSymlinks(t.TempDir())
	sub := filepath.Join(top, "svc")
	writeFile(t, filepath.Join(top, ".rota", "repos.json"), `{"repos":[{"path":"`+sub+`"}]}`)
	writeFile(t, filepath.Join(sub, ".rota", "x"), "")
	_, err := findRoot(sub, true)
	if !errors.Is(err, rotatree.ErrMasked) {
		t.Fatalf("nameless absolute entry: err = %v, want ErrMasked", err)
	}
}

// Ctx.Project is the one root+config read of an invocation: asking twice loads
// the config once, so preflight and the verb share the value.
func TestCtxProjectLoadsConfigOnce(t *testing.T) {
	top, _ := filepath.EvalSymlinks(t.TempDir())
	writeFile(t, filepath.Join(top, ".rota", "x"), "")
	d := testDeps()
	d.Getwd = func() (string, error) { return top, nil }
	calls := 0
	d.Config = func(string) any { calls++; return map[string]any{"k": "v"} }
	c := &Ctx{Deps: d}
	for i := 0; i < 3; i++ {
		root, cfg, err := c.Project()
		if err != nil || root != top || cfg == nil {
			t.Fatalf("Project = %q, %v, %v; want %q and a config", root, cfg, err, top)
		}
	}
	if calls != 1 {
		t.Errorf("Config called %d times, want 1", calls)
	}
}

// A missing root surfaces ErrNoRoot through Project, and a retry repeats it
// without loading a config.
func TestCtxProjectMissingRoot(t *testing.T) {
	top, _ := filepath.EvalSymlinks(t.TempDir())
	d := testDeps()
	d.Getwd = func() (string, error) { return top, nil }
	calls := 0
	d.Config = func(string) any { calls++; return nil }
	c := &Ctx{Deps: d}
	if _, _, err := c.Project(); err == nil {
		t.Fatalf("Project err = %v, want the no-root error", err)
	}
	if calls != 0 {
		t.Errorf("Config called %d times for a missing root", calls)
	}
}

// Verbs whose root comes from a helper read config through projectConfig: it
// shares the value Project loaded for the same root and reloads for another.
func TestCtxProjectConfigSharedAcrossRoots(t *testing.T) {
	top, _ := filepath.EvalSymlinks(t.TempDir())
	writeFile(t, filepath.Join(top, ".rota", "x"), "")
	d := testDeps()
	d.Getwd = func() (string, error) { return top, nil }
	var seen []string
	d.Config = func(r string) any { seen = append(seen, r); return map[string]any{"k": r} }
	c := &Ctx{Deps: d}
	if _, _, err := c.Project(); err != nil {
		t.Fatal(err)
	}
	c.projectConfig(top)
	c.projectConfig(top)
	if len(seen) != 1 {
		t.Fatalf("Config calls for one root = %v, want 1", seen)
	}
	c.projectConfig("/other")
	if len(seen) != 2 || seen[1] != "/other" {
		t.Errorf("Config calls = %v, want a reload for the second root", seen)
	}
}
