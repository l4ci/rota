package worker

import (
	"errors"
	"os"
	"testing"

	"github.com/l4ci/rota/internal/fsio"
)

// A truncated workers.json holds the only record of who owns what; Update must
// not replace it with an empty pool (#579).
func TestUpdateRefusesACorruptRegistry(t *testing.T) {
	root := t.TempDir()
	p := RegistryPath(root)
	if err := os.MkdirAll(p[:len(p)-len("/workers.json")], 0o755); err != nil {
		t.Fatal(err)
	}
	const bad = `{"slots": [{"name": "ben"`
	if err := os.WriteFile(p, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Update(root, func(d *Doc) { d.SetSlots(nil) })
	if !errors.Is(err, fsio.ErrUnreadable) {
		t.Fatalf("err = %v, want ErrUnreadable", err)
	}
	if b, _ := os.ReadFile(p); string(b) != bad {
		t.Errorf("registry was overwritten: %q", b)
	}
}
