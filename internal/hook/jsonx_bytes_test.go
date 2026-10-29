package hook

import (
	"encoding/json"
	"github.com/l4ci/rota/internal/fsio/fsiotest"
	"path/filepath"
	"testing"
)

func TestWriteStateWritesJsonxBytes(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.json")
	if err := writeState(p, State{SessionID: "s", Cwd: fsiotest.Hostile, Raw: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	fsiotest.RequireJsonxBytes(t, p)
}
