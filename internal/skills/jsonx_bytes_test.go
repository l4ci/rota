package skills

import (
	"testing"

	"github.com/l4ci/rota/internal/fsio/fsiotest"
)

func TestWriteManifestWritesJsonxBytes(t *testing.T) {
	root := t.TempDir()
	if err := writeManifest(root, Manifest{Schema: 1, Version: fsiotest.Hostile, Files: map[string]string{"a": "b"}}); err != nil {
		t.Fatal(err)
	}
	fsiotest.RequireJsonxBytes(t, manifestPath(root))
}
