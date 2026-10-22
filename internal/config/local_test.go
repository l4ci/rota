package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
)

func localRoot(t *testing.T, local string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".rota", "config.json"), []byte(`{"issues":{"source":"github"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if local != "" {
		if err := os.WriteFile(filepath.Join(root, ".rota", "config.local.json"), []byte(local), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// #1774: the support path's answers merge into config.local.json; keys
// already there survive and config.json is never written.
func TestSaveLocalMerges(t *testing.T) {
	root := localRoot(t, `{"round":{"role":"main","workerKind":"claude"},"keep":1}`+"\n")
	before, _ := os.ReadFile(filepath.Join(root, ".rota", "config.json"))
	if err := SaveLocal(root, map[string]string{"round.role": "support", "round.dispatch": "tmux", "autonomy.level": "off"}); err != nil {
		t.Fatal(err)
	}
	local := fsio.LoadJSON(filepath.Join(root, ".rota", "config.local.json"), nil)
	for k, want := range map[string]any{"round.role": "support", "round.dispatch": "tmux", "round.workerKind": "claude", "autonomy.level": "off"} {
		if got, _ := Lookup(local, k); got != want {
			t.Errorf("%s = %v, want %v", k, got, want)
		}
	}
	if got, _ := local.(*jsonx.Object).Get("keep"); got == nil {
		t.Error("an unrelated local key was dropped")
	}
	if after, _ := os.ReadFile(filepath.Join(root, ".rota", "config.json")); string(after) != string(before) {
		t.Errorf("config.json changed:\n%s", after)
	}
}

func TestSaveLocalCreatesFile(t *testing.T) {
	root := localRoot(t, "")
	if err := SaveLocal(root, map[string]string{"round.role": "support"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, ".rota", "config.local.json"))
	if err != nil || !strings.Contains(string(raw), `"role": "support"`) {
		t.Errorf("config.local.json = %s (%v)", raw, err)
	}
}

func TestSaveLocalRefusesBeforeWriting(t *testing.T) {
	root := localRoot(t, "")
	err := SaveLocal(root, map[string]string{"round.role": "support", "autonomy.level": "sideways"})
	if !errors.Is(err, ErrBadValue) {
		t.Errorf("bad value: err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".rota", "config.local.json")); err == nil {
		t.Error("a rejected answer wrote config.local.json")
	}
	broken := localRoot(t, "{not json")
	if err := SaveLocal(broken, map[string]string{"round.role": "support"}); err == nil {
		t.Error("an unparseable config.local.json was overwritten")
	}
	if raw, _ := os.ReadFile(filepath.Join(broken, ".rota", "config.local.json")); string(raw) != "{not json" {
		t.Errorf("config.local.json = %s", raw)
	}
}
