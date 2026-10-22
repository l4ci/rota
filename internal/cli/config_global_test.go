package cli

import (
	"bytes"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/tui"
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
	// --yes pins no taste key (#1786): the project inherits the global value,
	// even an invalid one, rather than setup writing the fallback over it.
	if raw := readConfig(t, dir); strings.Contains(raw, "isolation") {
		t.Errorf("--yes pinned work.isolation:\n%s", raw)
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
	writeFile(t, config.GlobalPath(gdir), `{"docs":{"path":"manual"}}`)
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
	if strings.Contains(string(raw), `"path"`) {
		t.Errorf("fill pinned a default under a corrupt global:\n%s", raw)
	}
}

func TestConfigGlobalVerbsWorkOutsideAProject(t *testing.T) {
	gdir := isolateGlobal(t)
	bare := t.TempDir() // no .rota/ here or above
	if code, _, e := initRun(t, bare, "config", "set", "--global", "work.isolation", "worktree"); code != 0 {
		t.Fatalf("set --global: %d %s", code, e)
	}
	raw, err := os.ReadFile(config.GlobalPath(gdir))
	if err != nil || !strings.Contains(string(raw), `"worktree"`) {
		t.Fatalf("global file (%v):\n%s", err, raw)
	}
	code, env, e := initRun(t, bare, "config", "show", "--global", "work.isolation")
	if code != 0 {
		t.Fatalf("show --global: %d %s", code, e)
	}
	row := initData(env)
	entries, _ := row.Get("entries")
	got := entries.([]any)[0].(*jsonx.Object)
	if src, _ := got.Get("source"); src != "global" {
		t.Errorf("source = %v, want global", src)
	}
	if v, _ := got.Get("value"); v != "worktree" {
		t.Errorf("value = %v, want worktree", v)
	}
	// a key the global does not set reports the default, not the project's
	_, env, _ = initRun(t, bare, "config", "show", "--global", "work.mergeStrategy")
	entries, _ = initData(env).Get("entries")
	if src, _ := entries.([]any)[0].(*jsonx.Object).Get("source"); src != "default" {
		t.Errorf("unset global key source = %v, want default", src)
	}
}

func TestConfigSetGlobalRefusesProjectOwnedKeys(t *testing.T) {
	isolateGlobal(t)
	for _, key := range []string{"git.baseBranch", "umbrella.enabled"} {
		if code, _, _ := initRun(t, t.TempDir(), "config", "set", "--global", key, "x"); code != 2 {
			t.Errorf("set --global %s exit %d, want 2", key, code)
		}
	}
	if code, _, _ := initRun(t, t.TempDir(), "config", "set", "--global", "work.contextGuard", "bogus"); code != 2 {
		t.Errorf("set --global with an invalid value exit %d, want 2", code)
	}
}

func TestConfigShowJSONCarriesGlobalPerKey(t *testing.T) {
	gdir := isolateGlobal(t)
	writeFile(t, config.GlobalPath(gdir), `{"work":{"isolation":"worktree","mergeStrategy":"pr"}}`)
	dir := t.TempDir()
	if code, _, e := initRun(t, dir, "init"); code != 0 {
		t.Fatalf("init: %d %s", code, e)
	}
	if _, err := config.Set(dir, "work.mergeStrategy", "direct"); err != nil {
		t.Fatal(err)
	}
	_, env, _ := initRun(t, dir, "config", "show")
	entries, _ := initData(env).Get("entries")
	byKey := map[string]*jsonx.Object{}
	for _, e := range entries.([]any) {
		o := e.(*jsonx.Object)
		k, _ := o.Get("key")
		byKey[k.(string)] = o
	}
	if src, _ := byKey["work.isolation"].Get("source"); src != "global" {
		t.Errorf("inherited key source = %v, want global", src)
	}
	// an overridden key still tells what it would inherit
	if g, _ := byKey["work.mergeStrategy"].Get("global"); g != "pr" {
		t.Errorf("overridden key global = %v, want pr", g)
	}
	if _, ok := byKey["ship.qa"].Get("global"); ok {
		t.Error("a key the global does not set carries a global value")
	}
}

func TestConfigScreenSwitchesInheritAndOverride(t *testing.T) {
	m, _ := play(newFxScreen(&fakeStore{rows: []cfgRow{
		{Key: "work.isolation", Source: "global", Group: "work", Type: "enum", Value: "worktree", Default: "branch", Global: "worktree", HasGlobal: true},
	}}), []string{"down", "g"})
	if r := m.(configScreen).rows[0]; r.Source != "project" {
		t.Errorf("after g on an inherited key: source %q, want project", r.Source)
	}
	m, _ = play(m, []string{"g"})
	if r := m.(configScreen).rows[0]; r.Source != "global" {
		t.Errorf("after g on an override: source %q, want global", r.Source)
	}
}

func TestGlobalWritesRefuseATruncatedGlobal(t *testing.T) {
	const truncated = `{"work":{"isolation":"worktree"},"ship":{"qa":true},`
	proj := t.TempDir()
	gdir := isolateGlobal(t)
	if code, _, e := initRun(t, proj, "init"); code != 0 {
		t.Fatalf("init: %d %s", code, e)
	}
	path := config.GlobalPath(gdir)
	for name, args := range map[string][]string{
		"set --global":   {"config", "set", "--global", "models.worker", "haiku"},
		"unset via edit": nil,
		"save-global":    {"config", "save-global"},
	} {
		if args == nil {
			continue
		}
		writeFile(t, path, truncated)
		code, _, _ := runMainWith(testDeps(), append([]string{"-C", proj}, args...)...)
		if code == 0 {
			t.Errorf("%s exit 0 on a truncated global", name)
		}
		if raw, _ := os.ReadFile(path); string(raw) != truncated {
			t.Errorf("%s changed the truncated global:\n%s", name, raw)
		}
	}
	writeFile(t, path, truncated)
	if err := (globalStore{gdir}).Reset("ship.qa"); err == nil {
		t.Error("Reset on a truncated global returned nil")
	}
	if raw, _ := os.ReadFile(path); string(raw) != truncated {
		t.Errorf("Reset changed the truncated global:\n%s", raw)
	}
	if code, _, _ := runMainWith(testDeps(), "-C", proj, "config", "show", "--global"); code != 70 {
		t.Errorf("show --global on a truncated global exit %d, want 70", code)
	}
}

func TestConfigScreenOnTheGlobalConfig(t *testing.T) {
	gdir := isolateGlobal(t)
	writeFile(t, config.GlobalPath(gdir), `{"work":{"isolation":"worktree"}}`)
	store := globalStore{gdir}
	rows, err := store.Rows()
	if err != nil {
		t.Fatal(err)
	}
	m := newConfigScreen(rows, store, new([]string))
	var got tui.Model = m
	for i, r := range rows {
		if r.Key == "work.isolation" {
			got, _ = play(got, append(rep("down", i+len(m.groups())), "g"))
			break
		}
	}
	if s := got.(configScreen); !strings.Contains(s.msg, "no project") {
		t.Errorf("g on the global screen: msg %q", s.msg)
	}
	for _, h := range got.(configScreen).hints() {
		if h.Key == "g" || h.Key == "l" {
			t.Errorf("global screen offers %q", h.Key)
		}
	}
	if err := store.Reset("work.isolation"); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(config.GlobalPath(gdir)); strings.Contains(string(raw), "isolation") {
		t.Errorf("reset left the key in the global:\n%s", raw)
	}
}

// #1603: save-global reports changed, true only when a global value differs.
func TestConfigSaveGlobalReportsChanged(t *testing.T) {
	isolateGlobal(t)
	src := t.TempDir()
	if code, _, e := initRun(t, src, "init"); code != 0 {
		t.Fatalf("init: %d %s", code, e)
	}
	if code, _, e := initRun(t, src, "config", "set", "work.isolation", "worktree"); code != 0 {
		t.Fatalf("set: %d %s", code, e)
	}
	changed := func() any {
		code, env, e := initRun(t, src, "config", "save-global")
		if code != 0 {
			t.Fatalf("save-global: %d %s", code, e)
		}
		v, ok := initData(env).Get("changed")
		if !ok {
			t.Fatalf("data has no changed field: %v", env)
		}
		return v
	}
	if got := changed(); got != true {
		t.Errorf("first save-global changed = %v, want true", got)
	}
	if got := changed(); got != false {
		t.Errorf("repeat save-global changed = %v, want false", got)
	}
	if code, _, e := initRun(t, src, "config", "set", "work.isolation", "none"); code != 0 {
		t.Fatalf("set: %d %s", code, e)
	}
	if got := changed(); got != true {
		t.Errorf("save-global after a value change: changed = %v, want true", got)
	}
}
