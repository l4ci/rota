package config

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestMergeGlobalKeepsExistingKeysAndDropsProjectOwned(t *testing.T) {
	dir := t.TempDir()
	seed := `{"ship":{"review":false},"custom":{"note":"keep"}}`
	if err := os.WriteFile(GlobalPath(dir), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	saved, err := MergeGlobal(dir, map[string]any{
		"work.isolation":   "worktree",
		"git.baseBranch":   "develop",
		"umbrella.enabled": true,
		"not.a.schema.key": "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"work.isolation"}; !reflect.DeepEqual(saved, want) {
		t.Errorf("saved = %v, want %v", saved, want)
	}
	raw, err := os.ReadFile(GlobalPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"ship":   map[string]any{"review": false},
		"custom": map[string]any{"note": "keep"},
		"work":   map[string]any{"isolation": "worktree"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("global file = %v, want %v", got, want)
	}
}
