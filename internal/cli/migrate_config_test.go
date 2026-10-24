package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/config"
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

// #1786: migrate config previews the scope step with the effective value
// before and after, --apply writes exactly that, a re-run plans nothing, and
// an empty tracked round.accounts no longer hides the global accounts.
func TestMigrateConfigScope(t *testing.T) {
	gdir := isolateGlobal(t)
	writeFile(t, config.GlobalPath(gdir), `{"round":{"accounts":[{"name":"g","configDir":"/g"}],"dispatch":"tmux"}}`)
	root := trackerProject(t, `{"round":{"accounts":[],"dispatch":"tmux","slots":3,"portBase":21000},"orchestrator":{"model":"sonnet"},"work":{"isolation":"branch","mergeStrategy":"pr"},"docs":{"path":"docs"}}`+"\n")
	local := filepath.Join(root, ".rota", "config.local.json")
	writeFile(t, local, `{"round":{"slots":2}}`+"\n")
	before := readConfig(t, root)
	code, out := textRun(t, "-C", root, "migrate", "config")
	for _, want := range []string{
		`delete round.dispatch (equals global; "tmux" -> "tmux")`,
		`delete round.accounts (equals default; [{"name": "g", "configDir": "/g"}] -> [{"name": "g", "configDir": "/g"}])`,
		`delete round.slots (config.local.json already holds 2; 2 -> 2)`,
		`move round.portBase -> config.local.json (21000 -> 21000)`,
		`move orchestrator.model -> config.local.json ("sonnet" -> "sonnet")`,
		`delete work.isolation (pinned at its default; "branch" -> "branch")`,
	} {
		if code != 0 || !strings.Contains(out, want) {
			t.Errorf("preview lacks %q: %d %q", want, code, out)
		}
	}
	if strings.Contains(out, "mergeStrategy") || strings.Contains(out, "docs.path") {
		t.Errorf("preview names a key that stays: %q", out)
	}
	if readConfig(t, root) != before {
		t.Error("the preview wrote config.json")
	}
	if code, out := textRun(t, "-C", root, "migrate", "config", "--apply"); code != 0 {
		t.Fatalf("apply: %d %q", code, out)
	}
	cfg := readConfig(t, root)
	for _, gone := range []string{`"round"`, `"orchestrator"`, "isolation"} {
		if strings.Contains(cfg, gone) {
			t.Errorf("config.json still holds %s:\n%s", gone, cfg)
		}
	}
	if !strings.Contains(cfg, `"mergeStrategy": "pr"`) || !strings.Contains(cfg, `"path": "docs"`) {
		t.Errorf("apply dropped a key that stays:\n%s", cfg)
	}
	lb, _ := os.ReadFile(local)
	for _, want := range []string{`"slots": 2`, `"portBase": 21000`, `"model": "sonnet"`} {
		if !strings.Contains(string(lb), want) {
			t.Errorf("config.local.json lacks %s: %s", want, lb)
		}
	}
	_, env, _ := rotaRun(t, "--json", "-C", root, "config", "show", "round.accounts")
	rows, _ := get(dataOf(env), "entries").([]any)
	if len(rows) != 1 || get(rows[0], "source") != "global" {
		t.Errorf("round.accounts after apply: %v", env)
	}
	if _, out := textRun(t, "-C", root, "migrate", "config"); !strings.Contains(out, "nothing to migrate") {
		t.Errorf("re-run after apply: %q", out)
	}

	// --keep leaves a key in place; a missing config.local.json is created
	// holding only the moved keys.
	root = trackerProject(t, `{"round":{"portBase":21000,"portBlock":50}}`+"\n")
	if code, out := textRun(t, "-C", root, "migrate", "config", "--apply", "--keep", "round.portBlock"); code != 0 {
		t.Fatalf("--keep: %d %q", code, out)
	}
	if cfg := readConfig(t, root); !strings.Contains(cfg, `"portBlock": 50`) || strings.Contains(cfg, "portBase") {
		t.Errorf("--keep: config.json\n%s", cfg)
	}
	if lb, _ := os.ReadFile(filepath.Join(root, ".rota", "config.local.json")); string(lb) != "{\n  \"round\": {\n    \"portBase\": 21000\n  }\n}\n" {
		t.Errorf("created config.local.json = %q", lb)
	}
}

// The scope step plans on the renamed view, so a preview that also renames
// old work.* names lists exactly what --apply then does (#1786 review).
func TestMigrateConfigPreviewMatchesApplyWithRename(t *testing.T) {
	isolateGlobal(t)
	root := trackerProject(t, `{"work":{"workerSlots":7,"dispatch":"subagent"}}`+"\n")
	_, pre, _ := rotaRun(t, "--json", "-C", root, "migrate", "config")
	_, app, _ := rotaRun(t, "--json", "-C", root, "migrate", "config", "--apply")
	for _, k := range []string{"config", "scope"} {
		p, _ := jsonx.MarshalCompact(get(dataOf(pre), k))
		a, _ := jsonx.MarshalCompact(get(dataOf(app), k))
		if string(p) != string(a) {
			t.Errorf("%s: preview %s, apply %s", k, p, a)
		}
	}
	scope, _ := get(dataOf(pre), "scope").([]any)
	if len(scope) != 2 {
		t.Errorf("preview scope = %v, want round.dispatch deleted and round.slots moved", scope)
	}
}

// #1824: migrate config folds the codex and opencode account keys into
// round.accounts in the global, tracked and local files; the preview lists
// each file without writing, --apply writes, a re-run plans nothing.
func TestMigrateConfigAccounts(t *testing.T) {
	gdir := isolateGlobal(t)
	writeFile(t, config.GlobalPath(gdir), `{"round":{"codexAccounts":[{"name":"g","codexHome":"/g"}]}}`)
	root := trackerProject(t, `{"round":{"opencodeAccounts":[{"name":"p","configDir":"/p"}]}}`+"\n")
	local := filepath.Join(root, ".rota", "config.local.json")
	writeFile(t, local, `{"round":{"accounts":[{"name":"l","configDir":"/l"}]}}`+"\n")

	code, out := textRun(t, "-C", root, "migrate", "config")
	for _, want := range []string{
		"global config.json\tround.codexAccounts -> round.accounts (1 entries)",
		"config.json\tround.opencodeAccounts -> round.accounts (1 entries)",
		"config.local.json\tround.accounts -> round.accounts (3 entries)",
	} {
		if code != 0 || !strings.Contains(out, want) {
			t.Errorf("preview lacks %q: %d %q", want, code, out)
		}
	}
	if b, _ := os.ReadFile(config.GlobalPath(gdir)); !strings.Contains(string(b), "codexAccounts") {
		t.Errorf("preview rewrote the global config: %s", b)
	}

	if code, out := textRun(t, "-C", root, "migrate", "config", "--apply"); code != 0 {
		t.Fatalf("apply: %d %q", code, out)
	}
	cfg := config.Load(filepath.Join(root, ".rota", "config.json"))
	want := []config.Account{{Kind: "claude", Name: "l", Dir: "/l"}, {Kind: "codex", Name: "g", Dir: "/g"}, {Kind: "opencode", Name: "p", Dir: "/p"}}
	if got := config.Accounts(cfg); !reflect.DeepEqual(got, want) {
		t.Errorf("accounts after apply = %v, want %v", got, want)
	}
	if b, _ := os.ReadFile(config.GlobalPath(gdir)); strings.Contains(string(b), "codexAccounts") {
		t.Errorf("global config still holds the old key: %s", b)
	}
	if code, out := textRun(t, "-C", root, "migrate", "config", "--apply"); code != 0 || !strings.Contains(out, "nothing to migrate") {
		t.Errorf("re-run: %d %q", code, out)
	}
}

// Two old keys naming one account with different dirs refuse with exit 4 and
// write nothing.
func TestMigrateConfigAccountsConflictRefuses(t *testing.T) {
	isolateGlobal(t)
	body := `{"round":{"codexAccounts":[{"name":"x","codexHome":"/c"}],"opencodeAccounts":[{"name":"x","configDir":"/o"}]}}` + "\n"
	root := trackerProject(t, body)
	code, env, _ := rotaRun(t, "--json", "-C", root, "migrate", "config", "--apply")
	if code != ExitRefused {
		t.Fatalf("exit %d, want %d: %v", code, ExitRefused, env)
	}
	if d := envObj(env, "data"); d["blockedBy"] != "account conflict" {
		t.Errorf("blockedBy = %v", d["blockedBy"])
	}
	if readConfig(t, root) != body {
		t.Errorf("config written despite the conflict: %s", readConfig(t, root))
	}
}
