package tracker

import (
	"context"
	"errors"
	"testing"
)

func TestParseKeyAndSpelling(t *testing.T) {
	for _, c := range []struct {
		in       string
		want     IssueKey
		ok       bool
		ref      string
		numbered int
	}{
		{"603", "603", true, "#603", 603},
		{"#603", "603", true, "#603", 603},
		{" 603 ", "603", true, "#603", 603},
		{"PROJ-123", "PROJ-123", true, "PROJ-123", 0},
		{"proj-123", "PROJ-123", true, "PROJ-123", 0},
		{"#eng-42", "ENG-42", true, "ENG-42", 0},
		{"B07", "", false, "", 0},
		{"PROJ-", "", false, "", 0},
		{"0", "", false, "", 0},
		{"", "", false, "", 0},
	} {
		k, ok := ParseKey(c.in)
		if k != c.want || ok != c.ok {
			t.Errorf("ParseKey(%q) = %q,%v want %q,%v", c.in, k, ok, c.want, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if k.Ref() != c.ref {
			t.Errorf("%q.Ref() = %q want %q", k, k.Ref(), c.ref)
		}
		if n, isNum := k.Number(); n != c.numbered || isNum != (c.numbered > 0) {
			t.Errorf("%q.Number() = %d,%v", k, n, isNum)
		}
	}
	if NumKey(603).Ref() != "#603" || NumKey(603).String() != "603" {
		t.Error("a numbered key prints as today: #603")
	}
}

func TestSortKeysNumbersFirstThenKeys(t *testing.T) {
	keys := []IssueKey{"PROJ-2", "10", "9", "ENG-42"}
	SortKeys(keys)
	want := []IssueKey{"9", "10", "ENG-42", "PROJ-2"}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("sorted = %v want %v", keys, want)
		}
	}
}

// AC-3: a key-based tracker closes an issue by its key in the head branch or
// the PR title; GitHub and GitLab by a keyword in the body.
func TestClosingConventions(t *testing.T) {
	key := IssueKey("PROJ-12")
	for _, c := range []struct {
		name string
		src  ClosingSource
		want bool
	}{
		{"title", ClosingSource{Title: "PROJ-12: widen the seam"}, true},
		{"branch", ClosingSource{Branch: "ben/PROJ-12-widen"}, true},
		{"lower case branch", ClosingSource{Branch: "ben/proj-12-widen"}, true},
		{"bracketed title", ClosingSource{Title: "[PROJ-12] widen"}, true},
		{"longer key", ClosingSource{Title: "PROJ-123 widen"}, false},
		{"longer prefix", ClosingSource{Title: "XPROJ-12 widen"}, false},
		{"body only", ClosingSource{Body: "Closes PROJ-12"}, false},
		{"empty", ClosingSource{}, false},
	} {
		if got := ClosesByKey(key, c.src); got != c.want {
			t.Errorf("%s: ClosesByKey = %v want %v", c.name, got, c.want)
		}
	}
	gh := &GitHub{base: base{closing: closingGH}}
	gl := &GitLab{base: base{closing: closingGL}}
	body := ClosingSource{Body: "Fixes #5\n", Title: "PROJ-5 title"}
	for _, a := range []IssueTracker{gh, gl} {
		if !a.ClosesIssue(NumKey(5), body) || a.ClosesIssue(NumKey(6), body) {
			t.Errorf("%s: a numbered issue closes by keyword", a.Provider())
		}
		if a.ClosesIssue("PROJ-5", body) {
			t.Errorf("%s: a key is not closed by a number's keyword or the title", a.Provider())
		}
	}
}

// GitHub and GitLab name issues by number: a Jira-style key is not an issue
// there, and the CLI is not called with it.
func TestNumberedAdaptersRefuseAKey(t *testing.T) {
	for _, a := range []Adapter{newAdapter(t, "github", nil), newAdapter(t, "gitlab", nil)} {
		_, err := a.Get(context.Background(), "PROJ-123", false)
		var e *Error
		if !errors.As(err, &e) || e.Kind != KindNotFound {
			t.Errorf("%s Get(PROJ-123) = %v, want KindNotFound", a.Provider(), err)
		}
	}
}
