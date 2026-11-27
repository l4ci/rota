package cycle_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/cycle"
	"github.com/l4ci/rota/internal/plan"
)

func TestClassifyStoredPlans(t *testing.T) {
	root := t.TempDir()
	store := plan.Files(root)
	for _, tc := range []struct {
		key  string
		path string
		want cycle.Kind
	}{
		{"M01-B07", ".rota/plans/M01-B07.md", cycle.Artifact},
		{"M02-B07", ".rota/plans/M02-B07.md", cycle.Artifact},
		{"M01-B08", ".rota/plans/M01-B08.md", cycle.User},
		{"M01-S01", ".rota/plans/M01-S01.md", cycle.User},
	} {
		t.Run(tc.key, func(t *testing.T) {
			ref, err := store.Resolve(tc.key)
			if err != nil {
				t.Fatal(err)
			}
			key, err := store.Create(ref, func(string) string { return "plan\n" })
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(root, tc.path)); err != nil {
				t.Fatal(err)
			}
			if got := cycle.Classify(tc.path, nil, []string{"B07"}); got != tc.want {
				t.Fatalf("stored plan %s: Classify(%q) = %v, want %v", key, tc.path, got, tc.want)
			}
		})
	}
}
