package rotatree

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func mkRota(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, DirName), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestFindRoot(t *testing.T) {
	top, _ := filepath.EvalSymlinks(t.TempDir())
	none := func(string) []string { return nil }

	deep := filepath.Join(top, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := FindRoot(deep, none); !errors.Is(err, ErrNoRoot) {
		t.Fatalf("no .rota/: err = %v, want ErrNoRoot", err)
	}

	mkRota(t, top)
	if got, err := FindRoot(deep, none); err != nil || got != top {
		t.Fatalf("FindRoot = %q, %v; want %q", got, err, top)
	}

	mkRota(t, filepath.Join(top, "a"))
	if got, err := FindRoot(deep, none); err != nil || got != filepath.Join(top, "a") {
		t.Fatalf("nearest: FindRoot = %q, %v", got, err)
	}
	registered := func(root string) []string {
		if root == top {
			return []string{"a"}
		}
		return nil
	}
	if _, err := FindRoot(deep, registered); !errors.Is(err, ErrMasked) {
		t.Fatalf("stray .rota/ in registered sub-repo: err = %v, want ErrMasked", err)
	}
}

func TestAtUmbrellaRoot(t *testing.T) {
	dir := t.TempDir()
	yes := func() bool { return true }
	if AtUmbrellaRoot(dir, yes) {
		t.Fatal("no .rota/: want false")
	}
	mkRota(t, dir)
	if !AtUmbrellaRoot(dir, yes) {
		t.Fatal(".rota/ and a registered sub-repo: want true")
	}
	if AtUmbrellaRoot(dir, func() bool { return false }) {
		t.Fatal("no registered sub-repo: want false")
	}
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if AtUmbrellaRoot(dir, yes) {
		t.Fatal("dir with its own .git: want false")
	}
}

// TestRootWalksStopAtTempRoot: a review tree or scratch dir under the project
// temp root (#2499) is no part of the project, so rota's upward root walks stop
// at .rota/tmp instead of reaching the live .rota/ above it.
func TestRootWalksStopAtTempRoot(t *testing.T) {
	top, _ := filepath.EvalSymlinks(t.TempDir())
	none := func(string) []string { return nil }
	mkRota(t, top)
	tree := filepath.Join(top, DirName, TmpDir, "rota-review-1", "tree", "pkg")
	if err := os.MkdirAll(tree, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, ok := Find(tree); ok {
		t.Fatalf("Find from under the temp root = %q; want no root", got)
	}
	if got, err := FindRoot(tree, none); !errors.Is(err, ErrNoRoot) {
		t.Fatalf("FindRoot from under the temp root = %q, %v; want ErrNoRoot", got, err)
	}
	if got, ok := Find(filepath.Join(top, DirName, TmpDir)); ok {
		t.Fatalf("Find at the temp root itself = %q; want no root", got)
	}
	// Elsewhere under .rota/ the project still resolves.
	plans := filepath.Join(top, DirName, PlansDir)
	if err := os.MkdirAll(plans, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, ok := Find(plans); !ok || got != top {
		t.Fatalf("Find from .rota/plans = %q, %v; want %q", got, ok, top)
	}
	// A tree that carries its own .rota/ resolves to itself.
	own := filepath.Dir(tree)
	mkRota(t, own)
	if got, err := FindRoot(tree, none); err != nil || got != own {
		t.Fatalf("FindRoot in a tree with its own .rota/ = %q, %v; want %q", got, err, own)
	}
}
