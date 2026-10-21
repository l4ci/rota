package worker

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathContains(t *testing.T) {
	real := t.TempDir()
	wt := filepath.Join(real, WorktreeRoot, "wt")
	sibling := filepath.Join(real, WorktreeRoot, "wt-2")
	for _, d := range []string{filepath.Join(wt, "sub"), sibling} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(t.TempDir(), "checkout")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(link, WorktreeRoot, "wt")
	for _, tc := range []struct {
		name          string
		parent, child string
		want          bool
	}{
		{"same path", wt, wt, true},
		{"descendant", wt, filepath.Join(wt, "sub"), true},
		{"component boundary", wt, sibling, false},
		{"parent is not inside child", filepath.Join(wt, "sub"), wt, false},
		{"symlinked child", wt, filepath.Join(linked, "sub"), true},
		{"symlinked parent", linked, filepath.Join(wt, "sub"), true},
		{"empty child is never contained", ".", "", false},
		{"empty parent contains nothing", "", wt, false},
		{"both empty", "", "", false},
	} {
		if got := PathContains(tc.parent, tc.child); got != tc.want {
			t.Errorf("%s: PathContains(%q, %q) = %v, want %v", tc.name, tc.parent, tc.child, got, tc.want)
		}
	}
}
