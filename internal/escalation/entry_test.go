package escalation

import (
	"reflect"
	"testing"
)

func TestEntryRoundTrip(t *testing.T) {
	root := t.TempDir()
	esc := Entry{ID: "e1", Kind: "pr", Number: 3, Slot: "dana", Title: "q", CommentID: "9",
		SentAt: "2026-10-03T10:00:00Z", Notified: true, Status: StatusPending,
		Options: []string{"a", "b"}, Recommend: 2, Cost: "a revert",
		Answer: &Answer{CommentID: "10", Author: "u", Body: "ok", SeenAt: "2026-10-03T11:00:00Z", Option: 2}}
	if err := Update(root, func(l []Entry) []Entry { return append(l, esc) }); err != nil {
		t.Fatal(err)
	}
	if got := Load(root); len(got) != 1 || !reflect.DeepEqual(got[0], esc) {
		t.Errorf("escalations = %+v, want [%+v]", got, esc)
	}
}
