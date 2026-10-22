package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/tui"
)

// #1786: config set writes a machine key to config.local.json by default,
// --global to the global config, --project to the tracked file with a
// warning; project and taste keys keep writing the tracked file, and --local
// sends any key to the local file.
func TestConfigSetRoutesByScope(t *testing.T) {
	gdir := isolateGlobal(t)
	root := trackerProject(t, "{}\n")
	local := filepath.Join(root, ".rota", "config.local.json")
	code, out := textRun(t, "-C", root, "config", "set", "round.slots", "3")
	if code != 0 || !strings.Contains(out, "wrote .rota/config.local.json (machine key") {
		t.Errorf("machine key: %d %q", code, out)
	}
	if lb, _ := os.ReadFile(local); !strings.Contains(string(lb), `"slots": 3`) {
		t.Errorf("config.local.json = %s", lb)
	}
	if strings.Contains(readConfig(t, root), "slots") {
		t.Errorf("a machine key reached config.json:\n%s", readConfig(t, root))
	}
	code, out, errOut := rotaIn(t, root, "config", "set", "--project", "round.slots", "4")
	if code != 0 || !strings.Contains(readConfig(t, root), `"slots": 4`) || !strings.Contains(errOut, "round.slots is a machine key") {
		t.Errorf("--project: %d %q %q", code, out, errOut)
	}
	if code, _ := textRun(t, "-C", root, "config", "set", "--global", "round.slots", "5"); code != 0 {
		t.Errorf("--global: %d", code)
	}
	if gb, _ := os.ReadFile(config.GlobalPath(gdir)); !strings.Contains(string(gb), `"slots": 5`) {
		t.Errorf("global = %s", gb)
	}
	for _, kv := range [][2]string{{"docs.path", "manual"}, {"work.isolation", "worktree"}} {
		if code, out := textRun(t, "-C", root, "config", "set", kv[0], kv[1]); code != 0 || strings.Contains(out, "machine key") {
			t.Errorf("%s: %d %q", kv[0], code, out)
		}
	}
	if cfg := readConfig(t, root); !strings.Contains(cfg, `"manual"`) || !strings.Contains(cfg, `"worktree"`) {
		t.Errorf("project and taste keys left config.json:\n%s", cfg)
	}
	if code, _ := textRun(t, "-C", root, "config", "set", "--local", "docs.path", "mine"); code != 0 {
		t.Errorf("--local: %d", code)
	}
	if lb, _ := os.ReadFile(local); !strings.Contains(string(lb), `"mine"`) {
		t.Errorf("--local missed config.local.json: %s", lb)
	}
	code, env, _ := rotaRun(t, "--json", "-C", root, "config", "show", "--local", "docs.path")
	if code != 0 {
		t.Fatalf("show --local: %d %v", code, env)
	}
	rows, _ := get(dataOf(env), "entries").([]any)
	if len(rows) != 1 || get(rows[0], "value") != "mine" || get(rows[0], "source") != "local" {
		t.Errorf("show --local: %d %v", code, env)
	}
	if code, _ := textRun(t, "-C", root, "config", "set", "--local", "--project", "docs.path", "x"); code != ExitUsage {
		t.Errorf("--local with --project: %d", code)
	}
	if code, _ := textRun(t, "-C", root, "config", "set", "--project", "round.role", "support"); code != ExitUsage {
		t.Errorf("--project round.role: %d", code)
	}
}

// filledProject is a tracker project whose tracked config holds every
// required key, so config check reads it as up to date.
func filledProject(t *testing.T, extra string) string {
	t.Helper()
	root := trackerProject(t, extra+"\n")
	if code, _ := textRun(t, "-C", root, "config", "fill"); code != 0 {
		t.Fatalf("fill: %d", code)
	}
	return root
}

// config check warns, exit 0, on a machine key in the tracked file and on a
// taste key pinned there at its default; a key the schema does not know
// fails it.
func TestConfigCheckWarnsOnScope(t *testing.T) {
	isolateGlobal(t)
	root := filledProject(t, `{"round": {"accounts": []}, "work": {"isolation": "branch", "mergeStrategy": "pr"}}`)
	code, env, _ := rotaRun(t, "--json", "-C", root, "config", "check")
	if dataOf(env) == nil {
		t.Fatalf("check: %d %v", code, env)
	}
	warns, _ := get(dataOf(env), "warnings").([]any)
	if code != 0 || len(warns) != 2 {
		t.Fatalf("check: %d %v", code, env)
	}
	code, out := textRun(t, "-C", root, "config", "check")
	for _, w := range []string{
		"warn: round.accounts is a machine key in .rota/config.json",
		"warn: work.isolation is pinned at its default in .rota/config.json",
	} {
		if code != 0 || !strings.Contains(out, w) {
			t.Errorf("check text lacks %q: %d %q", w, code, out)
		}
	}
	if strings.Contains(out, "mergeStrategy") {
		t.Errorf("a taste key pinned off its default warned: %q", out)
	}

	root = filledProject(t, `{"debug": {"competingHypotheses": true}}`)
	code, env, _ = rotaRun(t, "--json", "-C", root, "config", "check")
	unknown, _ := get(dataOf(env), "unknown").([]any)
	if code != ExitFailed || len(unknown) != 1 || unknown[0] != "debug.competingHypotheses" {
		t.Errorf("unknown key: %d %v", code, env)
	}
	if _, out := textRun(t, "-C", root, "config", "check"); !strings.Contains(out, "unknown: debug.competingHypotheses") {
		t.Errorf("unknown text: %q", out)
	}
}

// A complete tracked file passes check with no global file and an empty
// local file.
func TestCheckPassesWithNoGlobalAndEmptyLocal(t *testing.T) {
	isolateGlobal(t)
	root := filledProject(t, "{}")
	writeFile(t, filepath.Join(root, ".rota", "config.local.json"), "{}\n")
	if code, out := textRun(t, "-C", root, "config", "check"); code != 0 || strings.Contains(out, "warn:") {
		t.Errorf("check: %d %q", code, out)
	}
}

// The config screen edits a machine key in config.local.json on enter, as
// config set writes it.
func TestConfigEditLayer(t *testing.T) {
	isolateGlobal(t)
	root := trackerProject(t, "{}\n")
	t.Setenv("TERM", "xterm")
	store := rootStore{root, defaultDeps().projectsEnv()}
	rows, err := store.Rows()
	if err != nil {
		t.Fatal(err)
	}
	var changed []string
	m := tui.Model(newConfigScreen(rows, store, &changed))
	m, _ = play(m, cat([]string{"/"}, typedKeys("round.slots"), []string{"enter", "enter", "backspace", "6", "enter", "esc"}))
	m, _ = play(m, cat([]string{"/"}, typedKeys("docs.path"), []string{"enter", "enter", "backspace", "x", "enter", "esc"}))
	_ = m
	lb, _ := os.ReadFile(filepath.Join(root, ".rota", "config.local.json"))
	if !strings.Contains(string(lb), `"slots": 6`) || strings.Contains(string(lb), "docs") {
		t.Errorf("config.local.json = %s", lb)
	}
	if cfg := readConfig(t, root); strings.Contains(cfg, "slots") || !strings.Contains(cfg, `"path": "docx"`) {
		t.Errorf("config.json:\n%s", cfg)
	}
}
