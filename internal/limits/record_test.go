package limits

import (
	"os"
	"path/filepath"
	"testing"
)

// goldenRegistry is workers.json as the pre-ListSection limits.Update wrote it
// (generated from origin/main's code), so the test pins the file format, not
// only the codec's own stability.
const goldenRegistry = "{\n  \"slots\": [],\n  \"limits\": [\n    {\n      \"id\": \"l1\",\n      \"session\": \"kit\",\n      \"window\": \"five_hour\",\n      \"source\": \"\",\n      \"detectedAt\": \"\",\n      \"resetsAt\": \"2026-10-03T15:00:00Z\",\n      \"action\": \"\",\n      \"status\": \"resumed\",\n      \"cycles\": 2,\n      \"resolvedAt\": \"2026-10-03T16:00:00Z\",\n      \"note\": \"n\"\n    },\n    {\n      \"id\": \"l2\",\n      \"session\": \"\",\n      \"window\": \"\",\n      \"source\": \"\",\n      \"detectedAt\": \"\",\n      \"action\": \"\",\n      \"status\": \"waiting\",\n      \"cycles\": 0\n    }\n  ]\n}\n"

// An identity update of a registry written by the old code leaves it byte for
// byte: the section codec must not reorder, drop or add keys.
func TestSectionIdentityUpdateKeepsRegistryBytes(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, ".rota", "workers.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(goldenRegistry), 0o666); err != nil {
		t.Fatal(err)
	}
	before := []byte(goldenRegistry)
	if err := Section.Update(root, func(l []Entry) []Entry {
		if len(l) != 2 {
			t.Errorf("read %d limits, want 2", len(l))
		}
		return l
	}); err != nil {
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
