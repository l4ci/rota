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
