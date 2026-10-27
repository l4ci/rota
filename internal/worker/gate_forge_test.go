package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/tracker"
)

// forge.provider local: the gate merges directly and never builds a forge, so
// it never looks for a PR (#797).
func TestGateLocalForgeMergesDirectlyAndNeverLooksForAPR(t *testing.T) {
	w := newWorld(t, "")
	w.setConfig(`{"forge":{"provider":"local"},"test":{"full":["true"]}}`)
	gitq(t, w.dir, "fetch", "-q", "origin", "w1:w1")
	e := w.env(false)
	e.Forge = func(provider, dir string, _ tracker.Settings) (Forge, error) {
		t.Errorf("the gate asked for a %s forge; a local forge has none", provider)
		return nil, nil
	}
	res, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main"})
	if err != nil || res.Verdict != GatePass || !res.Changed || !w.onMain("work.txt") {
		t.Fatalf("%+v %v", res, err)
	}
	if res.PR != "" {
		t.Errorf("a PR was found or recorded: %q", res.PR)
	}
}

// With no origin remote, auto is local too.
func TestGateAutoForgeWithNoRemoteIsLocal(t *testing.T) {
	w := newWorld(t, "")
	gitq(t, w.dir, "fetch", "-q", "origin", "w1:w1")
	gitq(t, w.dir, "remote", "remove", "origin")
	e := w.env(false)
	e.Forge = func(provider, dir string, _ tracker.Settings) (Forge, error) {
		t.Errorf("the gate asked for a %s forge with no remote", provider)
		return nil, nil
	}
	res, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main"})
	if err != nil || res.Verdict != GatePass || !w.onMain("work.txt") {
		t.Fatalf("%+v %v", res, err)
	}
}

// A slot that records a PR cannot be gated against a local forge: merging it
// locally would bypass the PR.
func TestGateLocalForgeRefusesARecordedPR(t *testing.T) {
	w := newWorld(t, "7")
	w.setConfig(`{"forge":{"provider":"local"},"test":{"full":["true"]}}`)
	res, err := w.gate(false, GateOpts{})
	if err != nil || res.Verdict != GateCheckBroke || !strings.Contains(res.Err, "the forge is local") || w.onMain("work.txt") {
		t.Fatalf("%+v %v", res, err)
	}
}

// A file backlog with forge.provider set still opens and gates PRs: the forge
// comes from forge.provider, not from issues.source or the origin host.
func TestGateFileSourceStillGatesPRsOnTheConfiguredForge(t *testing.T) {
	for _, c := range []struct{ name, cfg, want string }{
		{"explicit gitlab, origin names no forge", `{"issues":{"source":"file"},"forge":{"provider":"gitlab"},"test":{"full":["true"]}}`, "gitlab"},
		{"explicit github", `{"issues":{"source":"file"},"forge":{"provider":"github"},"test":{"full":["true"]}}`, "github"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, "7")
			w.setConfig(c.cfg)
			var got string
			e := w.env(false)
			inner := e.Forge
			e.Forge = func(provider, dir string, s tracker.Settings) (Forge, error) {
				got = provider
				return inner(provider, dir, s)
			}
			res, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main"})
			if err != nil || res.Verdict != GatePass || !w.onMain("work.txt") {
				t.Fatalf("%+v %v", res, err)
			}
			if got != c.want {
				t.Errorf("forge provider = %q, want %q", got, c.want)
			}
		})
	}
}

// An origin naming no forge, with forge.provider auto, is the actor refusal:
// exit 4, blockedBy forge, nothing landed and no forge built.
func TestGateUnknownOriginRefusesBlockedByForge(t *testing.T) {
	w := newWorld(t, "7")
	w.setConfig(`{"issues":{"source":"file"},"test":{"full":["true"]}}`)
	const odd = "https://git.example.com/o/r.git"
	gitq(t, w.dir, "remote", "set-url", "origin", odd)
	e := w.env(false)
	e.Forge = func(provider, dir string, _ tracker.Settings) (Forge, error) {
		t.Errorf("the gate built a %s forge for an origin naming none", provider)
		return nil, nil
	}
	res, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main"})
	if err != nil || res.Verdict != GateForge || !res.Refused(err) || ClassifyVerdict(res.Verdict).BlockedBy != "forge" ||
		!strings.Contains(res.Err, "cannot determine the forge from origin "+odd) || w.onMain("work.txt") {
		t.Fatalf("%+v %v", res, err)
	}
}

// detectProvider adds one rule to tracker.ResolveProvider, a PR URL names its
// own forge, and reads origin through Env.Git for the kind it is given.
func TestDetectProvider(t *testing.T) {
	w := newWorld(t, "")
	gitq(t, w.dir, "remote", "set-url", "origin", "https://git.example.com/o/r.git")
	e := w.env(false).WithDefaults()
	ctx := context.Background()
	for _, c := range []struct {
		pr, configured string
		kind           tracker.ForgeKind
		want           string
	}{
		{glURL, "local", tracker.ForgeActor, "gitlab"}, // a PR URL names its own forge
		{"7", "github", tracker.ForgeActor, "github"},  // a bare number names none
		{"", "auto", tracker.ForgeReader, "github"},    // the origin names no forge
		{"", "auto", tracker.ForgeActor, ""},
	} {
		got, err := e.detectProvider(ctx, w.dir, c.pr, c.configured, c.kind)
		if got != c.want || (err != nil) != (c.want == "") {
			t.Errorf("pr %q configured %q kind %d: %q, %v; want %q", c.pr, c.configured, c.kind, got, err, c.want)
		}
	}
}

// A corrupt forge.provider stops the gate with exit 70 instead of reading as auto.
func TestGateInvalidForgeProviderIsACorruptConfig(t *testing.T) {
	w := newWorld(t, "7")
	w.setConfig(`{"forge":{"provider":"bitbucket"},"test":{"full":["true"]}}`)
	_, err := w.gate(false, GateOpts{})
	var ee *exitcode.Error
	if !errors.As(err, &ee) || ee.Exit != exitcode.ExitInternal || !strings.Contains(err.Error(), "invalid forge.provider") {
		t.Fatalf("%v", err)
	}
}

// issueStub is an issue tracker that is not the forge: it answers Get with the
// labels it was given and records what it was asked.
type issueStub struct {
	labels []string
	gets   []tracker.IssueKey
	keyed  bool // closes by key in the branch or title (Jira, Linear), not by keyword
}

func (s *issueStub) Get(_ context.Context, n tracker.IssueKey, _ bool) (tracker.Issue, error) {
	s.gets = append(s.gets, n)
	return tracker.Issue{Key: n, State: "open", Labels: s.labels}, nil
}
func (s *issueStub) RemoveLabels(context.Context, tracker.IssueKey, []string) error { return nil }
func (s *issueStub) ClosesIssue(k tracker.IssueKey, c tracker.ClosingSource) bool {
	if s.keyed {
		return tracker.ClosesByKey(k, c)
	}
	return slices.Contains(strings.Split(c.Body, "\n"), "Closes #"+k.String())
}

// The closing check reads the issue source, not the forge: with issues on a
// tracker other than the forge's own, the PR body "Refs #5" passes only
// because that tracker says #5 carries the partial-slice label.
func TestGateClosingCheckReadsTheIssueSource(t *testing.T) {
	for _, c := range []struct {
		name    string
		labels  []string
		verdict string
	}{
		{"partial slice on the tracker", []string{PartialSliceLabel}, GatePass},
		{"no partial slice on the tracker", nil, GateNotClosing},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, ghURL)
			w.setConfig(`{"issues":{"source":"gitlab"},"test":{"full":["true"]}}`) // a github forge, issues elsewhere
			os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"),
				[]byte(fmt.Sprintf(`{"slots":[{"name":"w1","branch":"w1","task":"#5","pr":%q}]}`, ghURL)), 0o644)
			w.setPRBody("Refs #5\n")
			w.forge("issueErr", "the forge must not be asked for the issue") // the forge's own Get fails
			stub := &issueStub{labels: c.labels}
			var gotSource string
			e := w.env(false)
			e.Issues = func(_ string, s tracker.Settings) (Issues, error) {
				gotSource = s.Source
				return stub, nil
			}
			res, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main"})
			if err != nil || res.Verdict != c.verdict {
				t.Fatalf("verdict = %q (%v), want %q: %+v", res.Verdict, err, c.verdict, res)
			}
			if gotSource != "gitlab" || len(stub.gets) == 0 || stub.gets[0] != tracker.NumKey(5) {
				t.Errorf("tracker built for %q, asked %v", gotSource, stub.gets)
			}
		})
	}
}

// AC-2/AC-3: the gate works with a key-based tracker (PROJ-123): the closing
// check accepts the key in the PR title or branch, not a `Closes #N` keyword.
func TestGateClosesByKeyOnAKeyBasedTracker(t *testing.T) {
	for _, c := range []struct {
		name, title, head, body string
		labels                  []string
		verdict                 string
	}{
		{name: "key in the title", title: "PROJ-123 widen the seam", head: "w1", verdict: GatePass},
		{name: "keyword alone does not close a key", title: "widen the seam", head: "w1", body: "Closes #123\n", verdict: GateNotClosing},
		{name: "a longer key is another issue", title: "PROJ-1234 widen", head: "w1", verdict: GateNotClosing},
		{name: "partial slice", title: "widen", head: "w1", labels: []string{PartialSliceLabel}, verdict: GatePass},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, ghURL)
			w.setConfig(`{"issues":{"source":"jira"},"test":{"full":["true"]}}`)
			os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"),
				[]byte(fmt.Sprintf(`{"slots":[{"name":"w1","branch":"w1","task":"PROJ-123","pr":%q}]}`, ghURL)), 0o644)
			w.setPRBody(c.body)
			w.forge("title", c.title)
			w.forge("head", c.head)
			stub := &issueStub{labels: c.labels, keyed: true}
			e := w.env(false)
			e.Issues = func(string, tracker.Settings) (Issues, error) { return stub, nil }
			res, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main"})
			if err != nil || res.Verdict != c.verdict {
				t.Fatalf("verdict = %q (%v), want %q: %+v", res.Verdict, err, c.verdict, res)
			}
			if res.Issue != "PROJ-123" {
				t.Errorf("issue = %q", res.Issue)
			}
			if c.verdict == GateNotClosing && !strings.Contains(res.Hint, "PROJ-123") {
				t.Errorf("hint names the key: %q", res.Hint)
			}
		})
	}
}
