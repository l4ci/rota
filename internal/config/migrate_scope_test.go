package config

import (
	"os"
	"reflect"
	"testing"
)

func TestMigrateScopePreviewAndApply(t *testing.T) {
	root := layerRig(t, `{"round":{"dispatch":"tmux"}}`,
		`{"round":{"dispatch":"tmux","slots":5,"roster":["a"]},"autonomy":{"level":"off"},"ship":{"review":"light"}}`, ``)
	m, err := MigrateScopeFiles(root, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []ScopeChange{
		{Key: "round.dispatch", Action: ScopeDelete, Reason: "equals global", Before: "tmux", After: "tmux"},
		{Key: "round.slots", Action: ScopeMove, Reason: "machine key", Before: num("5"), After: num("5")},
		{Key: "autonomy.level", Action: ScopeDelete, Reason: "pinned at its default", Before: "off", After: "off"},
	}
	if !reflect.DeepEqual(m.Changes, want) {
		t.Fatalf("plan %+v", m.Changes)
	}
	if _, err := os.Stat(localPath(root)); err == nil {
		t.Fatal("the preview wrote config.local.json")
	}
	if _, err := MigrateScopeFiles(root, true, nil); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, localPath(root)); got != "{\n  \"round\": {\n    \"slots\": 5\n  }\n}\n" {
		t.Errorf("local = %q", got)
	}
	// A value off its default and the global stays a project choice: roster
	// is a project key, ship.review a taste key off its default.
	if got := readFile(t, configPath(root)); got != "{\n  \"round\": {\n    \"roster\": [\n      \"a\"\n    ]\n  },\n  \"ship\": {\n    \"review\": \"light\"\n  }\n}\n" {
		t.Errorf("config = %q", got)
	}
	if m, _ := MigrateScopeFiles(root, false, nil); m.Changed() {
		t.Errorf("a second run plans %+v", m.Changes)
	}
}
