package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/config"
)

// isolateGlobal points the rota config dir at a fresh temp dir and returns it.
func isolateGlobal(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "rota")
	t.Setenv("ROTA_CONFIG_DIR", dir)
	return dir
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

func TestConfigSaveGlobalThenInitInherits(t *testing.T) {
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
		t.Errorf("work.isolation = %v, want inherited worktree", v)
	}
	if v := cfgValue(t, dst, "ship.review"); v != false {
		t.Errorf("ship.review = %v, want inherited false", v)
	}
	if v := cfgValue(t, dst, "work.mergeStrategy"); v != "direct" {
		t.Errorf("work.mergeStrategy = %v, want schema default", v)
	}
}

func TestInitInheritsGlobalWithoutCopyingIt(t *testing.T) {
	gdir := isolateGlobal(t)
	writeFile(t, config.GlobalPath(gdir), `{"work":{"isolation":"worktree"}}`)
	dir := t.TempDir()
	if code, _, e := initRun(t, dir, "init"); code != 0 {
		t.Fatalf("init: %d %s", code, e)
	}
	raw, err := os.ReadFile(filepath.Join(dir, ".rota", "config.json"))
	if err != nil || strings.Contains(string(raw), "isolation") {
		t.Errorf("init copied the global key into config.json (%v):\n%s", err, raw)
	}
	rows, err := config.Show(dir, "work.isolation", true)
	if err != nil || len(rows) != 1 || rows[0].Source != "global" || rows[0].Value != "worktree" {
		t.Errorf("show = %v, %v", rows, err)
	}
}

func TestProjectKeyBeatsGlobalAfterInit(t *testing.T) {
	gdir := isolateGlobal(t)
	dir := t.TempDir()
	if code, _, e := initRun(t, dir, "init"); code != 0 {
		t.Fatalf("init: %d %s", code, e)
	}
	if _, err := config.Set(dir, "work.isolation", "branch"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, config.GlobalPath(gdir), `{"work":{"isolation":"worktree"}}`)
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
	var warn bytes.Buffer
	old := config.WarnWriter
	config.WarnWriter = &warn
	t.Cleanup(func() { config.WarnWriter = old })
	code, _, errOut := initRun(t, dir, "init")
	if code != 0 {
		t.Errorf("want success, got %d %q", code, errOut)
	}
	if !strings.Contains(warn.String(), "global config skipped") {
		t.Errorf("no corrupt-global warning, got %q", warn.String())
	}
	if v := cfgValue(t, dir, "work.isolation"); v != "branch" {
		t.Errorf("work.isolation = %v", v)
	}
}

func TestSaveGlobalKeepsInheritedKeys(t *testing.T) {
	gdir := isolateGlobal(t)
	writeFile(t, config.GlobalPath(gdir), `{"work":{"isolation":"worktree"},"ship":{"review":"none"}}`)
	dir := t.TempDir()
	if code, _, e := initRun(t, dir, "init"); code != 0 {
		t.Fatalf("init: %d %s", code, e)
	}
	if code, _, e := initRun(t, dir, "config", "save-global"); code != 0 {
		t.Fatalf("save-global: %d %s", code, e)
	}
	raw, err := os.ReadFile(config.GlobalPath(gdir))
	if err != nil || !strings.Contains(string(raw), `"worktree"`) || !strings.Contains(string(raw), `"none"`) {
		t.Errorf("save-global dropped inherited keys (%v):\n%s", err, raw)
	}
}

func TestConfigCheckNamesACorruptGlobal(t *testing.T) {
	gdir := isolateGlobal(t)
	writeFile(t, config.GlobalPath(gdir), `{"work":{"isolation":"worktree"}}`)
	dir := t.TempDir()
	if code, _, e := initRun(t, dir, "init"); code != 0 {
		t.Fatalf("init: %d %s", code, e)
	}
	writeFile(t, config.GlobalPath(gdir), `{not json`)
	code, out, _ := runMainWith(testDeps(), "-C", dir, "config", "check")
	if code != 1 || !strings.Contains(out, "STALE:") || !strings.Contains(out, "GLOBAL CORRUPT") {
		t.Errorf("check = %d %q", code, out)
	}
	// fill must not pin defaults over the global while it cannot be read.
	code, fout, ferr := runMainWith(testDeps(), "-C", dir, "config", "fill")
	if code != 0 {
		t.Fatalf("fill: %d %s", code, ferr)
	}
	if strings.Contains(fout, "up to date") || !strings.Contains(fout, "GLOBAL CORRUPT") {
		t.Errorf("fill must name the corrupt global, not claim up to date: %q", fout)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, ".rota", "config.json"))
	if strings.Contains(string(raw), "isolation") {
		t.Errorf("fill pinned a default under a corrupt global:\n%s", raw)
	}
}
