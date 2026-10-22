package config

import "testing"

// Every schema row names the layer it belongs in (#1786).
func TestEveryKeyHasAScope(t *testing.T) {
	for _, k := range Keys {
		switch k.Scope {
		case ScopeProject, ScopeMachine, ScopeTaste:
		default:
			t.Errorf("%s: no scope", k.Name)
		}
	}
}

// init and fill write Required keys to the tracked file, so only a project
// key may be Required: a machine or taste key written there shadows the
// global config.
func TestRequiredOnlyOnProjectKeys(t *testing.T) {
	for _, k := range Keys {
		if k.Required && k.Scope != ScopeProject {
			t.Errorf("%s: Required on a %s key", k.Name, k.Scope)
		}
	}
}

// The audit's anchors: a key from each scope lands where #1786 puts it.
func TestScopeOf(t *testing.T) {
	for key, want := range map[string]Scope{
		"round.accounts":   ScopeMachine,
		"models.worker":    ScopeMachine,
		"work.isolation":   ScopeTaste,
		"issues.source":    ScopeProject,
		"git.baseBranch":   ScopeProject,
		"not.a.schema.key": "",
	} {
		if got := ScopeOf(key); got != want {
			t.Errorf("ScopeOf(%s) = %q, want %q", key, got, want)
		}
	}
}
