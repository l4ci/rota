package fsio

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSymlinkIn(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	for _, dir := range []string{"plain/deep", "nested"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "plain/file"), []byte("safe"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{
		"link": outside, "nested/link": outside,
		"leaf": filepath.Join(outside, "absent"),
	} {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ rel, want string }{
		{"plain/file", ""}, {"plain/deep/missing", ""}, {"missing/deep/file", ""}, {".", ""},
		{"link/child/file", "link"}, {"nested/link/file", "nested/link"},
		{"leaf", "leaf"}, {"nested/link/../file", "nested/link"},
	} {
		t.Run(tc.rel, func(t *testing.T) {
			got, err := SymlinkIn(root, tc.rel)
			want := ""
			if tc.want != "" {
				want = filepath.Join(root, tc.want)
			}
			if err != nil || got != want {
				t.Fatalf("SymlinkIn(%q) = %q, %v; want %q, nil", tc.rel, got, err, want)
			}
		})
	}
	t.Run("root is excluded", func(t *testing.T) {
		alias := filepath.Join(t.TempDir(), "root")
		if err := os.Symlink(root, alias); err != nil {
			t.Fatal(err)
		}
		if got, err := SymlinkIn(alias, "plain/file"); got != "" || err != nil {
			t.Fatalf("got %q, %v; root itself must not be checked", got, err)
		}
	})
	t.Run("filesystem error", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root bypasses directory permissions")
		}
		dir := filepath.Join(root, "denied")
		if err := os.Mkdir(dir, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(dir, 0o755) })
		if got, err := SymlinkIn(root, "denied/file"); got != "" || !errors.Is(err, os.ErrPermission) {
			t.Fatalf("got %q, %v; want permission error", got, err)
		}
	})
}
