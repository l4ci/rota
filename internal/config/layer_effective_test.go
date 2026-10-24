package config

import (
	"reflect"
	"testing"
)

// A required key only config.local.json or the global sets is not missing
// (#1765): check reads the layers `config show` does.
func TestCheckCountsLocalAndGlobalLayers(t *testing.T) {
	root := layerRig(t,
		`{"docs":{"path":"doc"}}`,
		`{"test":{"fast":[]}}`,
		`{"learn":{"promoteThreshold":2}}`)
	_, missing := Check(root)
	for _, key := range []string{"learn.promoteThreshold", "docs.path"} {
		if contains(missing, key) {
			t.Errorf("%s reported missing though a layer sets it: %v", key, missing)
		}
	}
	if !contains(missing, "test.full") {
		t.Errorf("test.full set nowhere must stay missing: %v", missing)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// An empty list in the project or local file is unset for the machine-specific
// list keys, so it does not shadow the global (#1766).
func TestEmptyAccountListFallsBackToGlobal(t *testing.T) {
	accounts := `[{"kind":"claude","name":"a","dir":"/x"}]`
	for _, key := range []string{"round.accounts"} {
		leaf := key[len("round."):]
		for _, layer := range []string{"project", "local"} {
			project, local := `{"round":{"`+leaf+`":[]}}`, ""
			if layer == "local" {
				project, local = "", project
			}
			root := layerRig(t, `{"round":{"`+leaf+`":`+accounts+`}}`, project, local)
			e := showOne(t, root, key)
			if e.Source != "global" {
				t.Errorf("%s empty in %s: source = %s, want global", key, layer, e.Source)
			}
			if list, _ := e.Value.([]any); len(list) != 1 {
				t.Errorf("%s empty in %s: value = %v, want the global list", key, layer, e.Value)
			}
			if v, _ := Lookup(Load(configPath(root)), key); !reflect.DeepEqual(v, e.Value) {
				t.Errorf("%s empty in %s: Load = %v, show = %v", key, layer, v, e.Value)
			}
		}
	}
}

// A non-empty project list still wins, and with no global the empty project
// list stays what it is: the explicit, configured empty.
func TestEmptyAccountListKeepsOtherSemantics(t *testing.T) {
	root := layerRig(t, `{"round":{"accounts":[{"name":"g"}]}}`, `{"round":{"accounts":[{"name":"p"}]}}`, "")
	if e := showOne(t, root, "round.accounts"); e.Source != "project" {
		t.Errorf("non-empty project list: source = %s, want project", e.Source)
	}
	root = layerRig(t, "", `{"round":{"accounts":[]}}`, "")
	if e := showOne(t, root, "round.accounts"); e.Source != "project" {
		t.Errorf("empty project list, no global: source = %s, want project", e.Source)
	}
	if _, missing := Check(root); contains(missing, "round.accounts") {
		t.Errorf("a written [] must still satisfy check: %v", missing)
	}
	root = layerRig(t, `{"round":{"accounts":[{"name":"g"}]}}`, `{"ship":{"review":false}}`, "")
	if e := showOne(t, root, "ship.review"); e.Value != false {
		t.Errorf("unrelated key changed: %v", e.Value)
	}
}
