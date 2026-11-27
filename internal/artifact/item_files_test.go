package artifact_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/l4ci/rota/internal/artifact"
)

func TestItemFileOwner(t *testing.T) {
	for _, tc := range []struct {
		path string
		want string
	}{
		{".rota/designs/B07.md", "B07"},
		{".rota/plans/M01-B07.md", "B07"},
		{".rota/plans/M02-B07.md", "B07"},
		{".rota/designs/B08.md", ""},
		{".rota/designs/M01-B07.md", ""},
		{".rota/plans/B07.md", ""},
		{".rota/plans/M01-B070.md", ""},
		{".rota/plans/M01-S01.md", ""},
		{".rota/plans/M01-B07.md.lock", ""},
		{".rota/plans/nested/M01-B07.md", ""},
		{"other/.rota/plans/M01-B07.md", ""},
		{"./.rota/plans/M01-B07.md", "B07"},
		{`.rota\plans\M01-B07.md`, "B07"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			if got := artifact.ItemFileOwner(tc.path, []string{"B01", "B07"}); got != tc.want {
				t.Fatalf("owner = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestItemFiles(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{
		".rota/designs/B07.md", ".rota/designs/B08.md",
		".rota/plans/M02-B07.md", ".rota/plans/M01-B07.md",
		".rota/plans/M01-B08.md", ".rota/plans/M01-S01.md",
		".rota/plans/B07.md", ".rota/plans/M01-B07.md.lock",
	} {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("note"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, ".rota/plans/M03-B07.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	want := []string{".rota/designs/B07.md", ".rota/plans/M01-B07.md", ".rota/plans/M02-B07.md"}
	if got := artifact.ItemFiles(root, "B07"); !reflect.DeepEqual(got, want) {
		t.Fatalf("files = %v, want %v", got, want)
	}
	if got := artifact.ItemFiles(root, "B99"); len(got) != 0 {
		t.Fatalf("unknown item has artifacts: %v", got)
	}
}
