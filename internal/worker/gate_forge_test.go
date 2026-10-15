package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
		{"auto, origin names no forge, falls back to github", `{"issues":{"source":"file"},"test":{"full":["true"]}}`, "github"},
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

func TestDetectProvider(t *testing.T) {
	w := newWorld(t, "")
	e := w.env(false).withDefaults()
	ctx := context.Background()
	set := func(url string) {
		if url == "" {
			gitq(t, w.dir, "remote", "remove", "origin")
			return
		}
		gitq(t, w.dir, "remote", "set-url", "origin", url)
	}
	for _, c := range []struct{ origin, pr, configured, want string }{
		{w.origin, "", "auto", "github"}, // an origin naming no forge: the gate's old assumption
		{"git@gitlab.com:o/r.git", "", "auto", "gitlab"},
		{"git@gitlab.com:o/r.git", "", "github", "github"}, // forge.provider beats the host
		{w.origin, "", "gitlab", "gitlab"},
		{w.origin, "", "local", "local"},
		{w.origin, glURL, "local", "gitlab"}, // a PR URL names its own forge
		{"", "", "auto", "local"},            // no remote
		{"", "", "gitlab", "gitlab"},
	} {
		set(c.origin)
		if got := e.detectProvider(ctx, w.dir, c.pr, c.configured); got != c.want {
			t.Errorf("origin %q pr %q configured %q: %q, want %q", c.origin, c.pr, c.configured, got, c.want)
		}
		if c.origin == "" {
			gitq(t, w.dir, "remote", "add", "origin", w.origin)
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
	gets   []int
}

func (s *issueStub) Get(_ context.Context, n int, _ bool) (tracker.Issue, error) {
	s.gets = append(s.gets, n)
	return tracker.Issue{Number: n, State: "open", Labels: s.labels}, nil
}
func (s *issueStub) RemoveLabels(context.Context, int, []string) error { return nil }

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
			w.forge("body", "Refs #5\n")
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
			if gotSource != "gitlab" || len(stub.gets) == 0 || stub.gets[0] != 5 {
				t.Errorf("tracker built for %q, asked %v", gotSource, stub.gets)
			}
		})
	}
}
