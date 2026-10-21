package round

import (
	"reflect"
	"testing"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// RoundCandidates owns the scope fallback: an explicit override, else the
// scope the registry recorded, else round.scope.
func TestRoundCandidatesScopeFallback(t *testing.T) {
	root := newRepo(t, nil)
	milestoneDoc(t, root, "M01", "active")
	be := &fakeRemote{}
	be.add("10", "in M01", "M01", false, "")
	be.add("12", "untagged a", "", false, "")
	be.add("13", "untagged b", "", false, "")
	e := Env{Git: git.Exec, Base: "main", Worker: worker.Env{Getenv: noEnv}, Lease: fakeLease("h", 100)}
	get := func(set roundcfg.Settings, override string) []string {
		t.Helper()
		cs, err := e.RoundCandidates(bg, root, be, set, override)
		if err != nil {
			t.Fatal(err)
		}
		return ids(cs)
	}

	// Nothing recorded: round.scope decides.
	if got := get(roundcfg.Settings{Scope: roundcfg.ScopeMilestone}, ""); !reflect.DeepEqual(got, []string{"10"}) {
		t.Errorf("config fallback: %v", got)
	}
	if got := get(roundcfg.Settings{Scope: roundcfg.ScopeOpen}, ""); !reflect.DeepEqual(got, []string{"10", "12", "13"}) {
		t.Errorf("config fallback, open: %v", got)
	}

	// A recorded slate beats the config scope.
	if _, err := e.Start(bg, root, startOpts(roundcfg.ScopeSlate, 100, "12")); err != nil {
		t.Fatal(err)
	}
	set := roundcfg.Settings{Scope: roundcfg.ScopeMilestone}
	if got := get(set, ""); !reflect.DeepEqual(got, []string{"12"}) {
		t.Errorf("recorded scope: %v", got)
	}
	if scope, slate := ScopeOf(root, set, ""); scope != roundcfg.ScopeSlate || !reflect.DeepEqual(slate, []string{"12"}) {
		t.Errorf("ScopeOf recorded: %q %v", scope, slate)
	}

	// An explicit override beats both.
	if got := get(set, roundcfg.ScopeMilestone); !reflect.DeepEqual(got, []string{"10"}) {
		t.Errorf("override: %v", got)
	}
	if scope, _ := ScopeOf(root, set, roundcfg.ScopeOpen); scope != roundcfg.ScopeOpen {
		t.Errorf("ScopeOf override: %q", scope)
	}
}

// A candidate says where its scope came from, so the orchestrator knows which
// ones to scout.
func TestCandidateScopeSource(t *testing.T) {
	root := newRepo(t, nil)
	be := &fakeRemote{}
	be.add("1", "declared", "", false, "## Touches\n- a\n")
	be.add("2", "bare", "", false, "prose")
	be.add("3", "scouted", "", false, "prose")
	if _, err := RecordInferred(be, "3", []string{"x"}); err != nil {
		t.Fatal(err)
	}
	e := Env{Git: git.Exec, Base: "main", Worker: worker.Env{Getenv: noEnv}, Lease: fakeLease("h", 100)}
	cs, err := e.RoundCandidates(bg, root, be, roundcfg.Settings{Scope: roundcfg.ScopeOpen}, "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range cs {
		got[c.ID] = c.ScopeSource
	}
	want := map[string]string{"1": SourceTouches, "2": SourceNone, "3": SourceInferred}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sources = %v want %v", got, want)
	}
}

// #1525 AC-1: an item filed by --file-deferred from a PR that is still open
// waits on that PR; once it is no longer open the item is ready again.
func TestCandidateDeferredFromOpenPRWaits(t *testing.T) {
	root := newRepo(t, nil)
	be := &fakeRemote{}
	deferred := "## Problem\n\nx\n\nDeferred minor finding from the review-quality PASS verdict on #77.\n\n## Acceptance\n\n- [ ] AC-1: fixed\n"
	be.add("20", "deferred from #77", "", false, deferred)
	be.items["20"].Fields.Related = "#77"
	be.add("21", "plain, relates to #77", "", false, "## Acceptance\n\n- [ ] AC-1: fixed\n")
	be.items["21"].Fields.Related = "#77"
	e := Env{Git: git.Exec, Base: "main", Forge: be.asForge(), Worker: worker.Env{Getenv: noEnv}, Lease: fakeLease("h", 100)}
	get := func() map[string]Candidate {
		t.Helper()
		cs, err := e.RoundCandidates(bg, root, be, roundcfg.Settings{Scope: roundcfg.ScopeOpen}, "")
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]Candidate{}
		for _, c := range cs {
			out[c.ID] = c
		}
		return out
	}
	be.prs = []tracker.PR{{Number: 77, Branch: "ben/9-other"}}
	got := get()
	if c := got["20"]; c.DependsOnPR != 77 || c.Ready() {
		t.Errorf("deferred item with PR #77 open: DependsOnPR=%d ready=%v", c.DependsOnPR, c.Ready())
	}
	if c := got["21"]; c.DependsOnPR != 0 || !c.Ready() {
		t.Errorf("an item that merely relates to #77 stays ready: %+v", c)
	}
	be.prs = nil
	if c := get()["20"]; c.DependsOnPR != 0 || !c.Ready() {
		t.Errorf("PR #77 merged: item is ready again: DependsOnPR=%d ready=%v", c.DependsOnPR, c.Ready())
	}
}
