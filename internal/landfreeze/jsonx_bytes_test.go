package landfreeze

import (
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/fsio/fsiotest"
)

func TestSaveWritesJsonxBytes(t *testing.T) {
	p := filepath.Join(t.TempDir(), "freeze.json")
	if err := Save(p, &Record{Active: true, Reason: fsiotest.Hostile}); err != nil {
		t.Fatal(err)
	}
	fsiotest.RequireJsonxBytes(t, p)
}
