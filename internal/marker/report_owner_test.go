package marker

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Report grammar belongs here; callers may keep literal whole-report fixtures.
func TestReportGrammarOwnedByMarker(t *testing.T) {
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Dir(path) == "../marker" || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(body), "rota:report") || strings.Contains(string(body), "marker.KindReport") {
			t.Errorf("%s owns report marker grammar outside marker", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
