package worker

import (
	"context"
	"strings"
	"testing"

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
