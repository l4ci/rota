package debugsession

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/l4ci/rota/internal/exitcode"
)

func exitOf(err error) int {
	if ae, ok := err.(*exitcode.Error); ok {
		return ae.Exit
	}
	return -1
}

func TestPutReplacesAndShowReadsBack(t *testing.T) {
	root := t.TempDir()
	if err := Put(root, "b07", "first\n"); err != nil {
		t.Fatal(err)
	}
	if err := Put(root, "b07", "second\n"); err != nil {
		t.Fatal(err)
	}
	got, err := Show(root, "b07")
	if err != nil || got != "second\n" {
		t.Fatalf("Show = %q, %v; want the replaced body", got, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".rota", "debug", "b07.md")); err != nil {
		t.Fatalf("session file not at .rota/debug/b07.md: %v", err)
	}
}

func TestUnknownSlugIsResolutionError(t *testing.T) {
	root := t.TempDir()
	if _, err := Show(root, "nope"); exitOf(err) != exitcode.ExitResolution {
		t.Fatalf("Show unknown: %v, want exit %d", err, exitcode.ExitResolution)
	}
	if err := Rm(root, "nope"); exitOf(err) != exitcode.ExitResolution {
		t.Fatalf("Rm unknown: %v, want exit %d", err, exitcode.ExitResolution)
	}
}

func TestBadSlugsRejected(t *testing.T) {
	root := t.TempDir()
	for _, slug := range []string{"a/b", "..", "../x", "a/../b", "", ".hidden", "x y"} {
		if err := Put(root, slug, "x"); exitOf(err) != exitcode.ExitUsage {
			t.Errorf("Put(%q) = %v, want usage exit", slug, err)
		}
		if _, err := Show(root, slug); exitOf(err) != exitcode.ExitUsage {
			t.Errorf("Show(%q) = %v, want usage exit", slug, err)
		}
		if err := Rm(root, slug); exitOf(err) != exitcode.ExitUsage {
			t.Errorf("Rm(%q) = %v, want usage exit", slug, err)
		}
	}
}

func TestListSkipsCounterFiles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".rota", "debug")
	if err := os.MkdirAll(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	// the Iron Law counter keeps <branch>.json beside the sessions
	if err := os.WriteFile(filepath.Join(dir, "feat-x.json"), []byte("{}"), 0o666); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"zeta", "alpha"} {
		if err := Put(root, s, "x"); err != nil {
			t.Fatal(err)
		}
	}
	got, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"alpha", "zeta"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("List = %v, want %v", got, want)
	}
}

func TestListEmptyWithoutDir(t *testing.T) {
	got, err := List(t.TempDir())
	if err != nil || len(got) != 0 {
		t.Fatalf("List = %v, %v; want empty", got, err)
	}
}

func TestRmDeletes(t *testing.T) {
	root := t.TempDir()
	if err := Put(root, "gone", "x"); err != nil {
		t.Fatal(err)
	}
	if err := Rm(root, "gone"); err != nil {
		t.Fatal(err)
	}
	if _, err := Show(root, "gone"); exitOf(err) != exitcode.ExitResolution {
		t.Fatalf("Show after Rm: %v", err)
	}
}
