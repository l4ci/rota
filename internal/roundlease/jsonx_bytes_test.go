package roundlease

import (
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/fsio/fsiotest"
)

func TestWriteWritesJsonxBytes(t *testing.T) {
	p := filepath.Join(t.TempDir(), "lease.json")
	if err := write(p, Lease{PID: 1, Root: fsiotest.Hostile}); err != nil {
		t.Fatal(err)
	}
	fsiotest.RequireJsonxBytes(t, p)
}
