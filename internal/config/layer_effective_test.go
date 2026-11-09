package config

import (
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
)

// Presence is checked in each supplying layer, independently of runtime merging.
// A complete, migration-free configuration must survive Fill byte-for-byte.
func TestCheckThenFillRequiredLayers(t *testing.T) {
	for _, tc := range []struct {
		name, key, project, global, local string
		missing                           bool
	}{
		{"project-only", "docs.path", `{"docs":{"path":"manual"}}`, "", "", false},
		{"local-only", "docs.path", "", "", `{"docs":{"path":"private-docs"}}`, false},
		{"global-only", "docs.path", "", `{"docs":{"path":"shared-docs"}}`, "", false},
		{"missing", "docs.path", "", "", `{"learn":{"promoteThreshold":2}}`, true},
		{"project null with global supplier", "docs.path", `{"docs":{"path":null}}`, `{"docs":{"path":"shared-docs"}}`, "", false},
		{"project scalar with local supplier", "docs.path", `{"docs":false}`, "", `{"docs":{"path":"private-docs"}}`, false},
		{"local null with project supplier", "docs.path", `{"docs":{"path":"manual"}}`, "", `{"docs":{"path":null}}`, false},
		{"local scalar with global supplier", "docs.path", "", `{"docs":{"path":"shared-docs"}}`, `{"docs":false}`, false},
		{"null everywhere", "docs.path", `{"docs":{"path":null}}`, `{"docs":{"path":null}}`, `{"docs":{"path":null}}`, true},
		{"scalar parents everywhere", "docs.path", `{"docs":false}`, `{"docs":false}`, `{"docs":false}`, true},
		{"local empty string", "docs.path", "", "", `{"docs":{"path":""}}`, false},
		{"local false", "umbrella.enabled", "", "", `{"umbrella":{"enabled":false}}`, false},
		{"local zero", "learn.promoteThreshold", "", "", `{"learn":{"promoteThreshold":0}}`, false},
		{"local empty list", "test.full", "", "", `{"test":{"full":[]}}`, false},
		{"global project-owned key excluded", "umbrella.enabled", "", `{"umbrella":{"enabled":true}}`, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := fullConfig()
			deletePath(cfg, splitDots(tc.key))
			if tc.project != "" {
				overlay, err := jsonx.Decode([]byte(tc.project))
				if err != nil {
					t.Fatal(err)
				}
				for _, key := range overlay.(*jsonx.Object).Keys() {
					value, _ := overlay.(*jsonx.Object).Get(key)
					cfg.Set(key, value)
				}
			}
			body, err := jsonx.MarshalCompact(cfg)
			if err != nil {
				t.Fatal(err)
			}
			root, env := layerRig(t, tc.global, string(body), tc.local)
			wantStatus, wantKeys := UpToDate, []string{}
			if tc.missing {
				wantStatus, wantKeys = Stale, []string{tc.key}
			}
			if status, missing := Check(env, root); status != wantStatus || !reflect.DeepEqual(missing, wantKeys) {
				t.Fatalf("Check = %s %v, want %s %v", status, missing, wantStatus, wantKeys)
			}
			filled, err := Fill(env, root)
			if err != nil || !reflect.DeepEqual(filled, wantKeys) {
				t.Fatalf("Fill = %v, %v, want %v", filled, err, wantKeys)
			}
			if !tc.missing {
				if got := read(t, root); got != string(body) {
					t.Errorf("complete project rewritten:\n%s", got)
				}
			} else {
				doc, err := jsonx.Decode([]byte(read(t, root)))
				if err != nil {
					t.Fatal(err)
				}
				value, present := Lookup(doc, tc.key)
				want := any("docs")
				if tc.key == "umbrella.enabled" {
					want = false
				}
				if !present || value != want {
					t.Errorf("filled %s = %v (present %v), want %v", tc.key, value, present, want)
				}
			}
			if status, missing := Check(env, root); status != UpToDate || len(missing) != 0 {
				t.Errorf("Check after Fill = %s %v", status, missing)
			}
			if tc.local != "" && readLocal(t, root) != tc.local {
				t.Errorf("local configuration changed: %s", readLocal(t, root))
			}
			if tc.global != "" {
				got, err := os.ReadFile(GlobalPath(env.Getenv("ROTA_CONFIG_DIR")))
				if err != nil || string(got) != tc.global {
					t.Errorf("global configuration changed: %s, %v", got, err)
				}
			}
		})
	}
}

func TestCheckThenFillCorruptSupplyingLayer(t *testing.T) {
	for _, layer := range []string{"global", "local"} {
		t.Run(layer, func(t *testing.T) {
			cfg := fullConfig()
			cfg.Delete("docs")
			body, err := jsonx.MarshalCompact(cfg)
			if err != nil {
				t.Fatal(err)
			}
			global, local := "{broken", ""
			if layer == "local" {
				global, local = "", "{broken"
			}
			root, env := layerRig(t, global, string(body), local)
			if status, missing := Check(env, root); status != Stale || !reflect.DeepEqual(missing, []string{"docs.path"}) {
				t.Fatalf("Check = %s %v, want stale with docs.path missing", status, missing)
			}
			filled, err := Fill(env, root)
			if layer == "local" {
				if !errors.Is(err, ErrCorrupt) {
					t.Errorf("Fill error = %v, want ErrCorrupt", err)
				}
			} else if err != nil {
				t.Errorf("Fill error = %v, want no error for a corrupt global", err)
			}
			if len(filled) != 0 || read(t, root) != string(body) {
				t.Errorf("Fill pinned defaults despite a corrupt %s: %v, %s", layer, filled, read(t, root))
			}
			path := GlobalPath(env.Getenv("ROTA_CONFIG_DIR"))
			if layer == "local" {
				path = localPath(root)
			}
			if got, err := os.ReadFile(path); err != nil || string(got) != "{broken" {
				t.Errorf("corrupt %s changed: %s, %v", layer, got, err)
			}
		})
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
	const key = "round.accounts"
	for _, layer := range []string{"project", "local"} {
		project, local := `{"round":{"accounts":[]}}`, ""
		if layer == "local" {
			project, local = "", project
		}
		root, env := layerRig(t, `{"round":{"accounts":`+accounts+`}}`, project, local)
		e := showOne(t, env, root, key)
		if e.Source != "global" {
			t.Errorf("%s empty in %s: source = %s, want global", key, layer, e.Source)
		}
		if list, _ := e.Value.([]any); len(list) != 1 {
			t.Errorf("%s empty in %s: value = %v, want the global list", key, layer, e.Value)
		}
		if v, _ := Lookup(Load(env, root), key); !reflect.DeepEqual(v, e.Value) {
			t.Errorf("%s empty in %s: Load = %v, show = %v", key, layer, v, e.Value)
		}
	}
}

// Layers merge round.accounts by kind (#1861): a layer that lists a kind
// replaces the lower layers' entries of that kind and inherits the other
// kinds, so a later edit to the global list still reaches the project.
func TestAccountsMergeByKind(t *testing.T) {
	root, env := layerRig(t,
		`{"round":{"accounts":[{"kind":"claude","name":"a1","dir":"/a1"},{"kind":"codex","name":"g","dir":"/g"}]}}`,
		`{"round":{"accounts":[{"kind":"codex","name":"c1","dir":"/c1"}]}}`,
		`{"round":{"accounts":[{"kind":"opencode","name":"o1","dir":"/o1"}]}}`)
	want := []Account{{KindClaude, "a1", "/a1"}, {KindCodex, "c1", "/c1"}, {KindOpencode, "o1", "/o1"}}
	if got := Accounts(Load(env, root)); !reflect.DeepEqual(got, want) {
		t.Errorf("effective accounts = %v, want %v", got, want)
	}
	e := showOne(t, env, root, "round.accounts")
	if list, _ := e.Value.([]any); len(list) != 3 {
		t.Errorf("show value = %v, want the merged three", e.Value)
	}
}

// A non-empty project list still wins, and with no global the empty project
// list stays what it is: the explicit, configured empty.
func TestEmptyAccountListKeepsOtherSemantics(t *testing.T) {
	root, env := layerRig(t, `{"round":{"accounts":[{"name":"g"}]}}`, `{"round":{"accounts":[{"name":"p"}]}}`, "")
	if e := showOne(t, env, root, "round.accounts"); e.Source != "project" {
		t.Errorf("non-empty project list: source = %s, want project", e.Source)
	}
	root, env = layerRig(t, "", `{"round":{"accounts":[]}}`, "")
	if e := showOne(t, env, root, "round.accounts"); e.Source != "project" {
		t.Errorf("empty project list, no global: source = %s, want project", e.Source)
	}
	if _, missing := Check(env, root); contains(missing, "round.accounts") {
		t.Errorf("a written [] must still satisfy check: %v", missing)
	}
	root, env = layerRig(t, `{"round":{"accounts":[{"name":"g"}]}}`, `{"ship":{"review":false}}`, "")
	if e := showOne(t, env, root, "ship.review"); e.Value != false {
		t.Errorf("unrelated key changed: %v", e.Value)
	}
}
