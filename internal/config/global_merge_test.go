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

// A failed value write must not leave the stamp this call wrote: the next fill
// would otherwise bless a pre-split auto as current (#1517).
func TestMergeGlobalRollsBackStampWhenValueWriteFails(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(GlobalPath(dir), []byte(`[1]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MergeGlobal(dir, map[string]any{"autonomy.level": "roundtrip"}); err == nil {
		t.Fatal("MergeGlobal on a non-object global: want an error")
	}
	if _, err := os.Stat(globalStampPath(dir)); !os.IsNotExist(err) {
		t.Errorf("stamp file survived the failed write (stat err = %v)", err)
	}
}

func TestMergeGlobalRollbackRestoresOlderStamp(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(GlobalPath(dir), []byte(`[1]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(globalStampPath(dir), []byte("0.0.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MergeGlobal(dir, map[string]any{"autonomy.level": "roundtrip"}); err == nil {
		t.Fatal("want an error")
	}
	b, _ := os.ReadFile(globalStampPath(dir))
	if string(b) != "0.0.1\n" {
		t.Errorf("stamp = %q, want the older stamp restored", b)
	}
}
