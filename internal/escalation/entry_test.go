package escalation

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestEntryRoundTrip(t *testing.T) {
	root := t.TempDir()
	esc := Entry{ID: "e1", Kind: "pr", Number: 3, Slot: "dana", Title: "q", CommentID: "9",
		SentAt: "2026-10-03T10:00:00Z", Notified: true, Status: StatusPending,
		Options: []string{"a", "b"}, Recommend: 2, Cost: "a revert",
		Answer: &Answer{CommentID: "10", Author: "u", Body: "ok", SeenAt: "2026-10-03T11:00:00Z", Option: 2}}
	if err := Section.Update(root, func(l []Entry) []Entry { return append(l, esc) }); err != nil {
		t.Fatal(err)
	}
	if got := Load(root); len(got) != 1 || !reflect.DeepEqual(got[0], esc) {
		t.Errorf("escalations = %+v, want [%+v]", got, esc)
	}
}

// goldenRegistry is workers.json as the pre-ListSection escalation.Update wrote
// it (generated from origin/main's code), so the test pins the file format, not
// only the codec's own stability.
const goldenRegistry = "{\n  \"slots\": [],\n  \"escalations\": [\n    {\n      \"id\": \"e1\",\n      \"kind\": \"pr\",\n      \"number\": 3,\n      \"slot\": \"dana\",\n      \"title\": \"q\",\n      \"commentId\": \"\",\n      \"sentAt\": \"\",\n      \"notified\": false,\n      \"options\": [\n        \"a\",\n        \"b\"\n      ],\n      \"recommend\": 2,\n      \"status\": \"pending\",\n      \"answer\": {\n        \"commentId\": \"10\",\n        \"author\": \"u\",\n        \"body\": \"ok\",\n        \"seenAt\": \"\",\n        \"option\": 2\n      }\n    },\n    {\n      \"id\": \"e2\",\n      \"kind\": \"\",\n      \"number\": 0,\n      \"title\": \"\",\n      \"commentId\": \"\",\n      \"sentAt\": \"\",\n      \"notified\": false,\n      \"status\": \"pending\"\n    }\n  ]\n}\n"

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
			t.Errorf("read %d escalations, want 2", len(l))
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
