package worker

import (
	"errors"
	"os"
	"path/filepath"
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

// LoadRegistry fails closed on a corrupt file; LoadRegistryTolerant is the
// explicit opt-out that reads it as an empty pool (#712).
func TestLoadRegistryStrictVersusTolerant(t *testing.T) {
	cases := []struct {
		name    string
		content *string // nil: no file
		wantErr bool
		exists  bool
		slots   int
	}{
		{name: "missing"},
		{name: "valid", content: ptr(`{"slots":[{"name":"ben"}]}`), exists: true, slots: 1},
		{name: "truncated", content: ptr(`{"slots": [{"name": "ben"`), wantErr: true},
		{name: "not an object", content: ptr(`["ben"]`), wantErr: true},
		{name: "empty file", content: ptr(``), wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			p := RegistryPath(root)
			if tc.content != nil {
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(*tc.content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			reg, err := LoadRegistry(root)
			if tc.wantErr {
				if !errors.Is(err, fsio.ErrUnreadable) {
					t.Fatalf("strict err = %v, want ErrUnreadable", err)
				}
			} else {
				if err != nil {
					t.Fatalf("strict err = %v", err)
				}
				if reg.Exists != tc.exists || len(reg.Slots()) != tc.slots {
					t.Errorf("strict: Exists=%v slots=%d", reg.Exists, len(reg.Slots()))
				}
			}
			if (CheckRegistry(root) != nil) != tc.wantErr {
				t.Errorf("CheckRegistry disagrees with LoadRegistry: %v", CheckRegistry(root))
			}
			tol := LoadRegistryTolerant(root)
			if tol.Exists != tc.exists || len(tol.Slots()) != tc.slots {
				t.Errorf("tolerant: Exists=%v slots=%d", tol.Exists, len(tol.Slots()))
			}
		})
	}
}

func ptr(s string) *string { return &s }
