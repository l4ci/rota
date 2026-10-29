package backlog

import (
	"errors"
	"github.com/l4ci/rota/internal/tracker"
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
		b, _ := newIssues(t, `{}`, Issue{Key: tracker.NumKey(1), Title: "First", State: "open", Labels: []string{"type:bug"}})
		return b, "1"
	}
	for _, c := range []struct {
		name  string
		open  func(*testing.T) (Backend, string)
		caps  Capabilities
		title string
	}{
		{"file", file, Capabilities{}, "First"},
		{"issues", issues, Capabilities{IssueIDs: true, ClaimWrites: true, Forge: true, ItemStatus: true}, "First"},
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

// TestCapabilityFlags pins every flag per backend, one subtest per flag, so a
// new backend or a flipped answer shows up as the flag that changed.
func TestCapabilityFlags(t *testing.T) {
	backends := []struct {
		name string
		caps Capabilities
		want map[string]bool
	}{
		{"file", (&File{}).Capabilities(), map[string]bool{}},
		{"issues", (&Issues{}).Capabilities(), map[string]bool{"IssueIDs": true, "ClaimWrites": true, "Forge": true, "ItemStatus": true}},
		{"umbrella", (&Umbrella{}).Capabilities(), map[string]bool{"IssueIDs": true, "ClaimWrites": true, "Forge": true, "Umbrella": true, "ItemStatus": true}},
	}
	for _, b := range backends {
		got := map[string]bool{
			"IssueIDs": b.caps.IssueIDs, "ClaimWrites": b.caps.ClaimWrites, "Forge": b.caps.Forge,
			"Umbrella": b.caps.Umbrella, "ItemStatus": b.caps.ItemStatus,
		}
		for flag, v := range got {
			t.Run(b.name+"/"+flag, func(t *testing.T) {
				if v != b.want[flag] {
					t.Errorf("%s.%s = %v, want %v", b.name, flag, v, b.want[flag])
				}
			})
		}
	}
}

// TestCapabilitiesFor: the config-only answer matches the opened backend's,
// so callers refuse a mode without opening one.
func TestCapabilitiesFor(t *testing.T) {
	for _, c := range []struct {
		source string
		want   Capabilities
	}{
		{"file", (&File{}).Capabilities()},
		{"github", (&Issues{}).Capabilities()},
	} {
		got, err := CapabilitiesFor(mustDecode(t, `{"issues":{"source":"`+c.source+`"}}`))
		if err != nil || got != c.want {
			t.Errorf("CapabilitiesFor(%s) = %+v, %v; want %+v", c.source, got, err, c.want)
		}
	}
}

// TestFileHoldsNoSharedClaims: a file-mode claim is a no-op that always wins,
// so it must not satisfy the interface that promises two holders.
func TestFileHoldsNoSharedClaims(t *testing.T) {
	var be Backend = &File{}
	if _, ok := be.(SharedClaimer); ok {
		t.Error("File satisfies SharedClaimer but cannot hold a second claim")
	}
	for _, be := range []Backend{&Issues{}, &Umbrella{}} {
		if _, ok := be.(SharedClaimer); !ok {
			t.Errorf("%T must satisfy SharedClaimer", be)
		}
	}
}
