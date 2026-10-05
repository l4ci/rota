package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/config"
)

// isolateGlobal points the rota config dir at a fresh temp dir and returns it.
func isolateGlobal(t *testing.T) string {
	t.Helper()
	x := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", x)
	return filepath.Join(x, "rota")
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestConfigSaveGlobalThenInitSeeds(t *testing.T) {
	gdir := isolateGlobal(t)
	src := t.TempDir()
	if code, _, e := initRun(t, src, "init"); code != 0 {
		t.Fatalf("init: %d %s", code, e)
	}
	for _, kv := range [][2]string{{"work.isolation", "worktree"}, {"ship.review", "false"}, {"git.baseBranch", "trunk"}} {
		if code, _, e := initRun(t, src, "config", "set", kv[0], kv[1]); code != 0 {
			t.Fatalf("set: %d %s", code, e)
		}
	}
	// config.local.json must not leak into the global file.
	writeFile(t, filepath.Join(src, ".rota", "config.local.json"), `{"work":{"mergeStrategy":"pr"}}`)
	if code, _, e := initRun(t, src, "config", "save-global"); code != 0 {
		t.Fatalf("save-global: %d %s", code, e)
	}
	raw, err := os.ReadFile(config.GlobalPath(gdir))
	if err != nil {
		t.Fatal(err)
	}
	g := string(raw)
	if !strings.Contains(g, `"worktree"`) || strings.Contains(g, `"pr"`) {
		t.Errorf("global file wrong:\n%s", g)
	}
	for _, bad := range []string{"trunk", "baseBranch", "rota"} {
		if strings.Contains(g, bad) {
			t.Errorf("global file holds project-owned %q:\n%s", bad, g)
		}
	}

	dst := t.TempDir()
	if code, _, e := initRun(t, dst, "init"); code != 0 {
		t.Fatalf("init dst: %d %s", code, e)
	}
	if v := cfgValue(t, dst, "work.isolation"); v != "worktree" {
		t.Errorf("work.isolation = %v, want seeded worktree", v)
	}
	if v := cfgValue(t, dst, "ship.review"); v != false {
		t.Errorf("ship.review = %v, want seeded false", v)
	}
	if v := cfgValue(t, dst, "work.mergeStrategy"); v != "direct" {
		t.Errorf("work.mergeStrategy = %v, want schema default", v)
	}
}

func TestInitDoesNotReseedExistingProjectNorLayerGlobal(t *testing.T) {
	gdir := isolateGlobal(t)
	dir := t.TempDir()
	if code, _, e := initRun(t, dir, "init"); code != 0 {
		t.Fatalf("init: %d %s", code, e)
	}
	writeFile(t, config.GlobalPath(gdir), `{"work":{"isolation":"worktree"}}`)
	// Re-init leaves the project alone, and config resolution ignores the global.
	if code, _, e := initRun(t, dir, "init"); code != 0 {
		t.Fatalf("re-init: %d %s", code, e)
	}
	if v := cfgValue(t, dir, "work.isolation"); v != "branch" {
		t.Errorf("work.isolation = %v, want project value branch", v)
	}
	rows, err := config.Show(dir, "work.isolation", true)
	if err != nil || len(rows) != 1 || rows[0].Source != "project" || rows[0].Value != "branch" {
		t.Errorf("show = %v, %v", rows, err)
	}
}

func TestSetupOffersGlobalAsDefault(t *testing.T) {
	gdir := isolateGlobal(t)
	writeFile(t, config.GlobalPath(gdir), `{"work":{"mergeStrategy":"pr","isolation":"bogus"}}`)
	dir := t.TempDir()
	// Enter takes every default: the global pr; the invalid isolation falls back.
	if code, _, e := setupRun(t, dir, "", false, "--yes"); code != 0 {
		t.Fatalf("setup: %d %s", code, e)
	}
	if v := cfgValue(t, dir, "work.mergeStrategy"); v != "pr" {
		t.Errorf("work.mergeStrategy = %v, want global pr", v)
	}
	if v := cfgValue(t, dir, "work.isolation"); v != "branch" {
		t.Errorf("work.isolation = %v, want schema default branch", v)
	}
	_, out, _ := setupRun(t, t.TempDir(), "", false, "--list")
	if !strings.Contains(out, "work.mergeStrategy") || !strings.Contains(out, "default pr") {
		t.Errorf("--list should show global default:\n%s", out)
	}
}

func TestInitWarnsOnCorruptGlobal(t *testing.T) {
	gdir := isolateGlobal(t)
	writeFile(t, config.GlobalPath(gdir), `{not json`)
	dir := t.TempDir()
	code, _, errOut := initRun(t, dir, "init")
	if code != 0 || !strings.Contains(errOut, "global config") {
		t.Errorf("want success with warning, got %d %q", code, errOut)
	}
	if v := cfgValue(t, dir, "work.isolation"); v != "branch" {
		t.Errorf("work.isolation = %v", v)
	}
}
