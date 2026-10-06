package backlog

import (
	"errors"
	"testing"
)

// TestBackendBehavior runs Get, Claim, Complete and title handling against
// both backends through the Backend and Workflow interfaces alone, so what a
// capability promises is checked without the cli in the way.
func TestBackendBehavior(t *testing.T) {
	file := func(t *testing.T) (Backend, string) {
		f, _ := proj(t, rotaFiles(nil))
		return f, "B01"
	}
	issues := func(t *testing.T) (Backend, string) {
		b, _ := newIssues(t, `{}`, Issue{Number: 1, Title: "First", State: "open", Labels: []string{"type:bug"}})
		return b, "1"
	}
	for _, c := range []struct {
		name  string
		open  func(*testing.T) (Backend, string)
		caps  Capabilities
		title string
	}{
		{"file", file, Capabilities{}, "First"},
		{"issues", issues, Capabilities{IssueIDs: true, ClaimWrites: true, Forge: true}, "First"},
	} {
		t.Run(c.name, func(t *testing.T) {
			be, id := c.open(t)
			if got := be.Capabilities(); got != c.caps {
				t.Fatalf("Capabilities = %+v, want %+v", got, c.caps)
			}

			it, err := be.Get(id)
			if err != nil {
				t.Fatal(err)
			}
			if it.ID != id || it.Title != c.title || it.Closed {
				t.Errorf("Get = %+v", it)
			}
			// IssueIDs is the mode test: a number exactly when IDs are issue numbers.
			if (it.Number != 0) != c.caps.IssueIDs {
				t.Errorf("Number = %d with IssueIDs %v", it.Number, c.caps.IssueIDs)
			}
			if _, err := be.Get("B99"); !errors.Is(err, ErrNotFound) {
				t.Errorf("Get unknown: %v", err)
			}

			wf, err := WorkflowOf(be)
			if err != nil {
				t.Fatal(err)
			}
			won, holder, err := wf.Claim(id, "ben@1")
			if err != nil || !won || holder != "ben@1" {
				t.Fatalf("Claim = %v, %q, %v", won, holder, err)
			}

			changed, err := be.Complete(id, CompleteInput{Commit: "abc", Date: "2026-10-06", Reason: "dropped", Note: "n"})
			if err != nil || !changed {
				t.Fatalf("Complete = %v, %v", changed, err)
			}
			if again, err := be.Complete(id, CompleteInput{Commit: "abc", Date: "2026-10-06", Reason: "dropped"}); err != nil || again {
				t.Errorf("Complete twice = %v, %v; want a no-op", again, err)
			}
			closed, err := be.Get(id)
			if err != nil || !closed.Closed || closed.Reason != "dropped" {
				t.Errorf("Get after Complete = %+v, %v", closed, err)
			}
		})
	}
}
