package worker

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/rotatree"
)

func TestLoadTrainCacheCorruptFileIsEmpty(t *testing.T) {
	root := t.TempDir()
	path := rotatree.TrainCache(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"verdicts": {"k": `), 0o644); err != nil {
		t.Fatal(err)
	}
	c := loadTrainCache(root)
	if len(c.f.Verdicts) != 0 || len(c.f.Culprits) != 0 || c.f.Verdicts == nil || c.f.Culprits == nil {
		t.Fatalf("corrupt cache = %+v, want empty non-nil maps", c.f)
	}
}
