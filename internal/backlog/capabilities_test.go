package backlog

import (
	"errors"
	"fmt"
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
		{"file", file, capabilitiesFor(kindFile), "First"},
		{"issues", issues, capabilitiesFor(kindIssues), "First"},
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

// TestCapabilitiesMatchInterfaces: a backend declares an optional capability
// exactly when it implements the interface behind it, so a new interface
// without a flag (or a flag without an implementation) fails here.
func TestCapabilitiesMatchInterfaces(t *testing.T) {
	probes := []struct {
		flag string
		has  func(Capabilities) bool
		impl func(Backend) bool
	}{
		{"SharedClaims", func(c Capabilities) bool { return c.SharedClaims }, func(b Backend) bool { _, ok := b.(SharedClaimer); return ok }},
		{"ClaimHistory", func(c Capabilities) bool { return c.ClaimHistory }, func(b Backend) bool { _, ok := b.(ClaimHistorian); return ok }},
		{"ClaimLabels", func(c Capabilities) bool { return c.ClaimLabels }, func(b Backend) bool { _, ok := b.(ClaimLabelSyncer); return ok }},
		{"BodyEdit", func(c Capabilities) bool { return c.BodyEdit }, func(b Backend) bool { _, ok := b.(BodyEditor); return ok }},
		{"SectionEdit", func(c Capabilities) bool { return c.SectionEdit }, func(b Backend) bool { _, ok := b.(SectionEditor); return ok }},
		{"HarnessField", func(c Capabilities) bool { return c.HarnessField }, func(b Backend) bool { _, ok := b.(HarnessSetter); return ok }},
		{"ItemStatus", func(c Capabilities) bool { return c.ItemStatus }, func(b Backend) bool { _, ok := b.(ItemTracker); return ok }},
	}
	for _, be := range []Backend{&File{}, &Issues{}, &Umbrella{}} {
		for _, p := range probes {
			t.Run(fmt.Sprintf("%T/%s", be, p.flag), func(t *testing.T) {
				if got, want := p.has(be.Capabilities()), p.impl(be); got != want {
					t.Errorf("declares %v, implements %v", got, want)
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

func TestCapabilitiesForSource(t *testing.T) {
	for src, issue := range map[string]bool{"": false, "file": false, "github": true, "jira": true} {
		if got := CapabilitiesForSource(src).IssueIDs; got != issue {
			t.Errorf("CapabilitiesForSource(%q).IssueIDs = %v, want %v", src, got, issue)
		}
	}
}
