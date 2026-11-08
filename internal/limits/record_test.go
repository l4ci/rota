package limits

import (
	"os"
	"path/filepath"
	"testing"
)

// An identity update rewrites the registry byte for byte: the section codec
// must not reorder, drop or add keys.
func TestSectionIdentityUpdateKeepsRegistryBytes(t *testing.T) {
	root := t.TempDir()
	lim := Entry{ID: "l1", Session: "kit", Window: "five_hour", ResetsAt: "2026-10-03T15:00:00Z", Cycles: 2, Status: StatusResumed, ResolvedAt: "2026-10-03T16:00:00Z", Note: "n"}
	if err := Section.Update(root, func(l []Entry) []Entry { return append(l, lim, Entry{ID: "l2", Status: StatusWaiting}) }); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, ".rota", "workers.json")
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := Section.Update(root, func(l []Entry) []Entry { return l }); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(p)
	if string(before) != string(after) {
		t.Errorf("registry bytes changed:\n%s\n--\n%s", before, after)
	}
}

func TestEntryRoundTrip(t *testing.T) {
	root := t.TempDir()
	lim := Entry{ID: "l1", Session: "kit", Window: "five_hour", ResetsAt: "2026-10-03T15:00:00Z", Cycles: 2, Status: StatusWaiting}
	if err := Section.Update(root, func(l []Entry) []Entry { return append(l, lim) }); err != nil {
		t.Fatal(err)
	}
	if got := Load(root); len(got) != 1 || got[0] != lim {
		t.Errorf("limits = %+v, want [%+v]", got, lim)
	}
}
