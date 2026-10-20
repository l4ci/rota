package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
)

// envObj is a nested envelope object as a map ("" keys absent).
func envObj(env map[string]any, key string) map[string]any {
	m := map[string]any{}
	if o, ok := env[key].(*jsonx.Object); ok {
		for _, k := range o.Keys() {
			m[k], _ = o.Get(k)
		}
	}
	return m
}

// legacyRoundProject is a file-backend project whose config.json still holds
// work.workerSlots and whose config.local.json holds work.accounts.
func legacyRoundProject(t *testing.T) string {
	t.Helper()
	root := trackerProject(t, `{"work":{"isolation":"branch","workerSlots":3}}`+"\n")
	local := filepath.Join(root, ".rota", "config.local.json")
	if err := os.WriteFile(local, []byte(`{"work":{"accounts":[{"name":"a","configDir":"/a"}]}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestRoundVerbsRefuseLegacyKeys(t *testing.T) {
	root := legacyRoundProject(t)
	for _, verb := range [][]string{
		{"round", "start"}, {"round", "assign", "B01", "--agent", "ben"}, {"round", "transfer", "B01", "--to", "dana"},
		{"round", "watch"}, {"round", "wait"}, {"round", "tick"}, {"round", "status"},
		{"worker", "gate", "ben"}, {"worker", "pool", "list"},
		{"doctor"}, {"layout"},
	} {
		t.Run(strings.Join(verb, " "), func(t *testing.T) {
			code, env, _ := rotaRun(t, append([]string{"--json", "-C", root}, verb...)...)
			if code != ExitRefused {
				t.Fatalf("exit %d, want %d: %v", code, ExitRefused, env)
			}
			data := envObj(env, "data")
			if data["blockedBy"] != "config migration" {
				t.Errorf("blockedBy = %v", data["blockedBy"])
			}
			e := envObj(env, "error")
			msg, _ := e["message"].(string)
			hint, _ := e["hint"].(string)
			msg += " " + hint
			for _, want := range []string{"work.workerSlots", "work.accounts", "rota migrate config"} {
				if !strings.Contains(msg, want) {
					t.Errorf("message lacks %s: %s", want, msg)
				}
			}
		})
	}
	// The prompt hook reads no config and must not block a worker's prompt.
	if code, _, _ := rotaRun(t, "-C", root, "worker", "prompt-check", "--key", filepath.Join(root, "nokey")); code == ExitRefused {
		t.Error("worker prompt-check refused over the config")
	}
}

// A worker verb run from a worktree cut before the rename reads none of the
// round keys, so a stale tracked config must not stop it (#1163).
func TestWorkerSlotVerbsIgnoreLegacyKeys(t *testing.T) {
	root := legacyRoundProject(t)
	for _, verb := range [][]string{
		{"worker", "done", "ben"}, {"worker", "inbox", "ben"},
		{"worker", "send", "ben", "--heartbeat"}, {"worker", "reply", "ben"},
	} {
		t.Run(strings.Join(verb, " "), func(t *testing.T) {
			_, env, errs := rotaRun(t, append([]string{"--json", "-C", root}, verb...)...)
			if data := envObj(env, "data"); data["blockedBy"] == "config migration" {
				t.Errorf("refused over legacy keys: %v", env)
			}
			if strings.Contains(errs, "rota migrate config") {
				t.Errorf("told to migrate config: %q", errs)
			}
		})
	}
}

func TestSoloVerbWarnsLegacyKeys(t *testing.T) {
	root := legacyRoundProject(t)
	code, _, errs := rotaRun(t, "--json", "-C", root, "item", "show", "B01")
	if code == ExitRefused {
		t.Fatalf("item show refused: %s", errs)
	}
	if !strings.Contains(errs, "work.workerSlots") || !strings.Contains(errs, "rota migrate config --apply") {
		t.Errorf("no migration warning on stderr: %q", errs)
	}
}

func TestConfigCheckFlagsOldRoundKey(t *testing.T) {
	root := trackerProject(t, `{"work":{"dispatch":"tmux"}}`+"\n")
	_, env, _ := rotaRun(t, "--json", "-C", root, "config", "check")
	data := envObj(env, "data")
	unknown, _ := data["unknown"].([]any)
	if len(unknown) != 1 || unknown[0] != "work.dispatch" {
		t.Errorf("unknown = %v, want [work.dispatch]", data["unknown"])
	}
	code, _, _ := rotaRun(t, "--json", "-C", root, "config", "set", "work.dispatch", "tmux")
	if code != ExitUsage {
		t.Errorf("config set work.dispatch exit %d, want %d (not a schema key)", code, ExitUsage)
	}
}

func TestMigrateConfigVerb(t *testing.T) {
	root := legacyRoundProject(t)
	cfgPath := filepath.Join(root, ".rota", "config.json")
	before, _ := os.ReadFile(cfgPath)
	code, env, _ := rotaRun(t, "--json", "-C", root, "migrate", "config")
	data := envObj(env, "data")
	if code != 0 || data["planned"] != true || data["changed"] != false {
		t.Fatalf("preview exit %d data %v", code, data)
	}
	if after, _ := os.ReadFile(cfgPath); string(after) != string(before) {
		t.Fatal("preview wrote config.json")
	}
	code, env, _ = rotaRun(t, "--json", "-C", root, "migrate", "config", "--apply")
	data = envObj(env, "data")
	if code != 0 || data["changed"] != true {
		t.Fatalf("apply exit %d data %v", code, data)
	}
	code, env, _ = rotaRun(t, "--json", "-C", root, "migrate", "config", "--apply")
	data = envObj(env, "data")
	if code != 0 || data["planned"] != false {
		t.Errorf("second run should have nothing to do: %v", data)
	}
	// Migrated: round verbs run again (status needs no host).
	if code, env, _ := rotaRun(t, "--json", "-C", root, "round", "status"); code == ExitRefused {
		t.Errorf("round status still refused after migration: %v", env)
	}
}

func TestMigrateConfigConflictRefuses(t *testing.T) {
	root := trackerProject(t, `{"work":{"workerSlots":3},"round":{"slots":5}}`+"\n")
	cfgPath := filepath.Join(root, ".rota", "config.json")
	before, _ := os.ReadFile(cfgPath)
	code, env, _ := rotaRun(t, "--json", "-C", root, "migrate", "config", "--apply")
	data := envObj(env, "data")
	if code != ExitRefused || data["blockedBy"] != "config conflict" {
		t.Fatalf("exit %d data %v, want refused with config conflict", code, data)
	}
	if after, _ := os.ReadFile(cfgPath); string(after) != string(before) {
		t.Error("a conflict wrote config.json")
	}
	if code, _, _ := rotaRun(t, "--json", "-C", root, "config", "fill"); code != ExitRefused {
		t.Errorf("config fill on a conflict exit %d, want %d", code, ExitRefused)
	}
}
