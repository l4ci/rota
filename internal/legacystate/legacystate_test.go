package legacystate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLegacyState(t *testing.T) {
	base := t.TempDir()
	os.MkdirAll(filepath.Join(base, "a", ".hv"), 0o777)
	os.MkdirAll(filepath.Join(base, "a", "b", "c"), 0o777)
	os.MkdirAll(filepath.Join(base, "m", ".hv"), 0o777)
	os.MkdirAll(filepath.Join(base, "m", ".rota"), 0o777)
	os.MkdirAll(filepath.Join(base, "m", "x"), 0o777)
	if d, ok := LegacyState(filepath.Join(base, "a", "b", "c")); !ok || d != filepath.Join(base, "a") {
		t.Errorf("legacy = %q %v", d, ok)
	}
	if _, ok := LegacyState(filepath.Join(base, "m", "x")); ok {
		t.Error("a dir with .rota/ is not legacy")
	}
	if _, ok := LegacyState(base); ok {
		t.Error("no state is not legacy")
	}
}
