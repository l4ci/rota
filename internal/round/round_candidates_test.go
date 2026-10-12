package round

import (
	"reflect"
	"testing"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/roundcfg"
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
