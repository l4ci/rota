package cli

import (
	"strings"
	"testing"
)

// passItemRef is the item resolution plan check and plan pass share: it reads
// an item reference once, keeps a tracker key whole and refuses a slice key.
func TestPassItemRef(t *testing.T) {
	for _, c := range []struct{ key, want string }{
		{"42", "42"}, {"#42", "42"}, {"B7", "B7"}, {"M01-B07", "B07"},
		{"PROJ-123", "PROJ-123"}, {"ENG-42", "ENG-42"}, {"STORY-12", "STORY-12"}, {"proj-123", "PROJ-123"},
	} {
		got, err := passItemRef("plan pass", c.key)
		if err != nil || got != c.want {
			t.Errorf("passItemRef(%q) = %q, %v; want %q", c.key, got, err, c.want)
		}
	}
	if _, err := passItemRef("plan pass", "M01-S02"); err == nil || !strings.Contains(err.Error(), "slice plan") {
		t.Errorf("slice key: %v, want slice refusal", err)
	}
	for _, key := range []string{"", "#", "x/y", "PROJ-", "PROJ-1-2", " 42", "S01", "M01"} {
		if _, err := passItemRef("plan pass", key); err == nil || !strings.Contains(err.Error(), "key must look like") {
			t.Errorf("passItemRef(%q): %v, want usage error", key, err)
		}
	}
}
