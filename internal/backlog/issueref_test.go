package backlog

import "testing"

// ParseIssueRef is the one reader of an unqualified item reference: numbers,
// lettered numbers and tracker keys, normalized to one spelling.
func TestParseIssueRef(t *testing.T) {
	for _, c := range []struct{ in, id, key, letter string }{
		{"42", "42", "42", ""}, {"#42", "42", "42", ""}, {"F07", "F07", "7", "F"}, {"f7", "F7", "7", "F"},
		{"PROJ-123", "PROJ-123", "PROJ-123", ""}, {"eng-42", "ENG-42", "ENG-42", ""}, {"STORY-12", "STORY-12", "STORY-12", ""},
	} {
		r, err := ParseIssueRef(c.in)
		if err != nil || r.ID != c.id || string(r.Key) != c.key || r.Letter != c.letter {
			t.Errorf("ParseIssueRef(%q) = %+v, %v; want id %q key %q letter %q", c.in, r, err, c.id, c.key, c.letter)
		}
	}
	for _, in := range []string{"", "#", "S01", "#B7", "PROJ-", "PROJ-1-2", " 42", "42 ", "# 42", "PROJ 1", "x/y", "M01-S02"} {
		if r, err := ParseIssueRef(in); err == nil {
			t.Errorf("ParseIssueRef(%q) = %+v, want an error", in, r)
		}
	}
}
