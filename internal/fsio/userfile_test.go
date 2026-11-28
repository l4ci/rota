package fsio

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteUserFile(t *testing.T) {
	read := func(t *testing.T, p string) string {
		t.Helper()
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	link := func(t *testing.T, target, name string) {
		t.Helper()
		if err := os.Symlink(target, name); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("chain through a linked directory", func(t *testing.T) {
		root := t.TempDir()
		real := filepath.Join(root, "real", "deep")
		if err := os.MkdirAll(real, 0o755); err != nil {
			t.Fatal(err)
		}
		link(t, real, filepath.Join(root, "alias"))
		// Relative to real/deep, so it names real/file, not root/file.
		link(t, "../file", filepath.Join(real, "inner"))
		link(t, filepath.Join("alias", "inner"), filepath.Join(root, "outer"))
		// The chain dangles on the first write and exists on the second.
		for _, want := range []string{"one", "two"} {
			if err := WriteUserFile(filepath.Join(root, "outer"), []byte(want)); err != nil {
				t.Fatal(err)
			}
			if got := read(t, filepath.Join(root, "real", "file")); got != want {
				t.Fatalf("target = %q, want %q", got, want)
			}
		}
		for _, p := range []string{filepath.Join(root, "outer"), filepath.Join(real, "inner")} {
			if fi, err := os.Lstat(p); err != nil || fi.Mode()&os.ModeSymlink == 0 {
				t.Errorf("%s is no longer a symlink: %v", p, err)
			}
		}
	})

	t.Run("loop", func(t *testing.T) {
		dir := t.TempDir()
		a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
		link(t, b, a)
		link(t, a, b)
		if err := WriteUserFile(a, []byte("x")); err == nil {
			t.Fatal("a symlink loop was written")
		}
	})
}
