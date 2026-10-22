package config

import (
	"reflect"
	"testing"
)

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

func TestSetRoutesMachineKeysToLocal(t *testing.T) {
	for _, c := range []struct {
		key            string
		project, local bool
		want           Layer
	}{
		{"round.slots", false, false, LayerLocal},
		{"round.slots", true, false, LayerProject},
		{"work.isolation", false, false, LayerProject},
		{"issues.source", false, false, LayerProject},
		{"issues.source", false, true, LayerLocal},
	} {
		if got := LayerFor(c.key, c.project, c.local); got != c.want {
			t.Errorf("LayerFor(%s, project=%v, local=%v) = %v, want %v", c.key, c.project, c.local, got, c.want)
		}
	}
}

// A required key set only in the local or global layer is not missing.
func TestCheckReadsMergedConfig(t *testing.T) {
	root := layerRig(t, `{"docs":{"path":"doc"}}`, `{}`, `{"learn":{"promoteThreshold":2}}`)
	_, missing := Check(root)
	if contains(missing, "docs.path") || contains(missing, "learn.promoteThreshold") {
		t.Errorf("missing %v", missing)
	}
}

func TestWarningsAndUnknown(t *testing.T) {
	root := layerRig(t, ``,
		`{"round":{"slots":3},"work":{"isolation":"branch","mergeStrategy":"pr"},"ship":{"review":{"default":"full"}},"backlog":{"backend":"file"},"debug":{"competingHypotheses":true},"models":5}`,
		`{"work":{"workerSlots":2},"zzz":{}}`)
	want := []string{"work.isolation is pinned at its default in .rota/config.json", "round.slots is a machine key in .rota/config.json"}
	if got := Warnings(root); !reflect.DeepEqual(got, want) {
		t.Errorf("Warnings = %v", got)
	}
	if got := Unknown(root); !reflect.DeepEqual(got, []string{"debug.competingHypotheses", "zzz" + LocalMark}) {
		t.Errorf("Unknown = %v", got)
	}
}
