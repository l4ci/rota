package fsio

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteTempLifecycle(t *testing.T) {
	consumerFailure := errors.New("consumer failed")
	for _, tc := range []struct {
		name string
		err  error
	}{{"success", nil}, {"consumer error", consumerFailure}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			var path string
			err := func() error {
				var cleanup func()
				var err error
				path, cleanup, err = WriteTemp(dir, "body-*.md", []byte("body\r\n\x00"))
				if err != nil {
					t.Fatal(err)
				}
				defer cleanup()
				if filepath.Dir(path) != dir || filepath.Ext(path) != ".md" {
					t.Fatalf("path = %q; want a .md file in %s", path, dir)
				}
				got, err := os.ReadFile(path)
				if err != nil || string(got) != "body\r\n\x00" {
					t.Fatalf("during call: body = %q, err = %v", got, err)
				}
				return tc.err
			}()
			if !errors.Is(err, tc.err) {
				t.Fatalf("consumer error = %v, want %v", err, tc.err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("after call: stat = %v; want file removed", err)
			}
		})
	}
}

func TestWriteTempCreateError(t *testing.T) {
	dir := t.TempDir()
	path, cleanup, err := WriteTemp(filepath.Join(dir, "missing"), "body-", []byte("body"))
	defer cleanup()
	if !os.IsNotExist(err) || path != "" {
		t.Fatalf("path = %q, err = %v; want missing directory error", path, err)
	}
	if files, err := os.ReadDir(dir); err != nil || len(files) != 0 {
		t.Fatalf("failed create left files %v, err = %v", files, err)
	}
}
