package backlog

import (
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/tracker"
	"slices"
	"strings"
	"testing"
)

// Hand-written expectations per backend, deliberately not derived from
// capabilitiesFor so a wrong flag there fails a test.
var (
	literalFile   = Capabilities{HarnessField: true}
	literalIssues = Capabilities{
		IssueIDs: true, ClaimWrites: true, Forge: true, ItemStatus: true,
		SharedClaims: true, ClaimHistory: true, ClaimLabels: true, BodyEdit: true, SectionEdit: true,
	}
	literalUmbrella = func() Capabilities { c := literalIssues; c.Umbrella = true; return c }()
)

// TestBackendBehavior runs Get, Claim, Complete and title handling against
// both backends through the Backend and Workflow interfaces alone, so what a
// capability promises is checked without the cli in the way.
func TestBackendBehavior(t *testing.T) {
	wantFile, wantIssues := literalFile, literalIssues
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
		{"file", file, wantFile, "First"},
		{"issues", issues, wantIssues, "First"},
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

// TestBlockedCompletion pins what Complete(reason=blocked) does on each
// backend, through the Backend interface alone. The two differ on purpose and
// the contract (docs/contributing/contract/backlog.md, item complete) says so:
// the file backend closes the bullet into ## Completed with a (blocked: note)
// mark; the issues backend leaves the issue open, adds the blocked label and a
// blocked comment. Neither runs the proof gate; repeating it is a no-op.
func TestBlockedCompletion(t *testing.T) {
	in := CompleteInput{Commit: "abc", Date: "2026-10-06", Reason: "blocked", Note: "waiting"}
	t.Run("file closes the bullet", func(t *testing.T) {
		f, _ := proj(t, rotaFiles(nil))
		var be Backend = f
		if changed, err := be.Complete("B01", in); err != nil || !changed {
			t.Fatalf("Complete = %v, %v", changed, err)
		}
		it, err := be.Get("B01")
		if err != nil || !it.Closed || it.Reason != "blocked" || it.Note != "waiting" {
			t.Errorf("Get = %+v, %v; want closed, reason blocked, note waiting", it, err)
		}
		if again, err := be.Complete("B01", in); err != nil || again {
			t.Errorf("Complete twice = %v, %v; want a no-op", again, err)
		}
		if !strings.Contains(read(t, f, ".rota/BACKLOG.md"), "Done 2026-10-06 [`abc`] (blocked: waiting)\n") {
			t.Error(read(t, f, ".rota/BACKLOG.md"))
		}
	})
	t.Run("issues keeps the issue open", func(t *testing.T) {
		b, _ := newIssues(t, `{}`, open(6))
		var be Backend = b
		if changed, err := be.Complete("6", in); err != nil || !changed {
			t.Fatalf("Complete = %v, %v", changed, err)
		}
		it, err := be.Get("6")
		if err != nil || it.Closed || !slices.Contains(it.Labels, "blocked") {
			t.Errorf("Get = %+v, %v; want open with the blocked label", it, err)
		}
		if again, err := be.Complete("6", in); err != nil || again {
			t.Errorf("Complete twice = %v, %v; want a no-op", again, err)
		}
	})
}

// TestCapabilitiesMatchInterfaces: a backend declares an optional capability
// exactly when it implements the interface behind it, so a new interface
// without a flag (or a flag without an implementation) fails here. The probe
// list must be extended by hand for each new capability flag.
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
		{"file", literalFile},
		{"github", literalIssues},
	} {
		got, err := CapabilitiesFor(mustDecode(t, `{"issues":{"source":"`+c.source+`"}}`))
		if err != nil || got != c.want {
			t.Errorf("CapabilitiesFor(%s) = %+v, %v; want %+v", c.source, got, err, c.want)
		}
	}
}

// TestBackendLiterals pins each backend's declared capabilities.
func TestBackendLiterals(t *testing.T) {
	for _, c := range []struct {
		name string
		be   Backend
		want Capabilities
	}{
		{"file", &File{}, literalFile},
		{"issues", &Issues{}, literalIssues},
		{"umbrella", &Umbrella{}, literalUmbrella},
	} {
		if got := c.be.Capabilities(); got != c.want {
			t.Errorf("%s Capabilities = %+v, want %+v", c.name, got, c.want)
		}
	}
}

// TestOptional: the flag gates the interface, in both directions.
func TestOptional(t *testing.T) {
	claims := func(c Capabilities) bool { return c.SharedClaims }
	if _, ok := Optional[SharedClaimer](nil, claims); ok {
		t.Error("nil backend resolved")
	}
	if _, ok := Optional[SharedClaimer](&Issues{}, claims); !ok {
		t.Error("flag true and interface implemented: want ok")
	}
	if _, ok := Optional[SharedClaimer](&File{}, claims); ok {
		t.Error("flag false, interface absent: want !ok")
	}
	// Flag false while the interface is implemented: Issues has no HarnessField.
	if _, ok := Optional[SharedClaimer](&Issues{}, func(Capabilities) bool { return false }); ok {
		t.Error("flag false with interface implemented resolved")
	}
}

func TestCapabilitiesForSource(t *testing.T) {
	for src, issue := range map[string]bool{"": false, "file": false, "github": true, "jira": true} {
		if got := CapabilitiesForSource(src).IssueIDs; got != issue {
			t.Errorf("CapabilitiesForSource(%q).IssueIDs = %v, want %v", src, got, issue)
		}
	}
}
