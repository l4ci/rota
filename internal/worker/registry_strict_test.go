package worker

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
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

// A non-object entry in a registry list is skipped on read and kept on write:
// one rule for every list the registry decodes.
func TestRegistryListsSkipNonObjectEntries(t *testing.T) {
	root := t.TempDir()
	p := RegistryPath(root)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	const doc = `{"slots":["junk",{"name":"ben"},7],"prs":[null,{"issue":"#1","pr":"#10"},"x",{"issue":"#2","pr":"#11"}]}`
	if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := LoadRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Slots(); len(got) != 1 || got[0].Name() != "ben" {
		t.Errorf("Slots = %v, want just ben", got)
	}
	if got := r.PRs(); len(got) != 2 || got[0].Issue != "#1" || got[1].Issue != "#2" {
		t.Errorf("PRs = %+v, want #1 and #2", got)
	}
	if err := Update(root, func(d *Doc) {
		d.DropQueued(func(q QueuedPR) bool { return q.Issue == "#1" })
		d.AppendSlot(AsSlot(jsonx.NewObject()))
	}); err != nil {
		t.Fatal(err)
	}
	r, _ = LoadRegistry(root)
	if got := r.PRs(); len(got) != 1 || got[0].Issue != "#2" {
		t.Errorf("after drop PRs = %+v, want #2", got)
	}
	if got := r.list("prs"); len(got) != 3 {
		t.Errorf("non-object entries must survive a write, got %d entries", len(got))
	}
	if got := r.list("slots"); len(got) != 4 {
		t.Errorf("slots entries = %d, want 4", len(got))
	}
}

// Slate skips non-string entries and SortSlots tolerates a junk entry; a
// tolerant read of the same file sees the same slots.
func TestRegistryListsSlateSortAndTolerantRead(t *testing.T) {
	root := t.TempDir()
	p := RegistryPath(root)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	const doc = `{"slate":["#1",7,null,"#2"],"slots":["junk",{"name":"zed"},{"name":"amy"}]}`
	if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	r := LoadRegistryTolerant(root)
	if got := r.Slate(); len(got) != 2 || got[0] != "#1" || got[1] != "#2" {
		t.Errorf("Slate = %v, want [#1 #2]", got)
	}
	if got := r.Slots(); len(got) != 2 {
		t.Errorf("tolerant Slots = %d, want 2", len(got))
	}
	if err := Update(root, func(d *Doc) { d.SortSlots() }); err != nil {
		t.Fatal(err)
	}
	r, _ = LoadRegistry(root)
	if got := r.Slots(); len(got) != 2 {
		t.Errorf("Slots after sort = %d, want 2", len(got))
	}
	if got := r.list("slots"); len(got) != 3 {
		t.Errorf("junk entry must survive SortSlots, got %d entries", len(got))
	}
}
