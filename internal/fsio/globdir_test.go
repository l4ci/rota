package fsio

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestGlobDirMatchesInDirWithMetacharacters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "re[po]*?x")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"b.md", "a.md", "c.txt", "M-S1.md"} {
		if err := os.WriteFile(filepath.Join(dir, n), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := GlobDir(dir, "*.md")
	want := []string{filepath.Join(dir, "M-S1.md"), filepath.Join(dir, "a.md"), filepath.Join(dir, "b.md")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GlobDir = %v, want %v", got, want)
	}
	if got := GlobDir(filepath.Join(dir, "missing"), "*.md"); len(got) != 0 {
		t.Fatalf("missing dir = %v, want empty", got)
	}
}
