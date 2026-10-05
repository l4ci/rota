package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/jsonx"
)

func initRun(t *testing.T, dir string, args ...string) (int, *jsonx.Object, string) {
	t.Helper()
	return initRunWith(t, testDeps(), dir, args...)
}

func initRunWith(t *testing.T, d *Deps, dir string, args ...string) (int, *jsonx.Object, string) {
	t.Helper()
	wd, _ := os.Getwd() // -C changes the process directory; put it back
	t.Cleanup(func() { os.Chdir(wd) })
	code, out, errOut := runMainWith(d, append([]string{"-C", dir}, append(args, "--json")...)...)
	v, err := jsonx.Decode([]byte(out))
	if err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
	return code, v.(*jsonx.Object), errOut
}

func initData(env *jsonx.Object) *jsonx.Object {
	d, _ := env.Get("data")
	o, _ := d.(*jsonx.Object)
	return o
}

func TestInitVerbSeedsAndRunsBlocks(t *testing.T) {
	dir := t.TempDir()
	code, env, _ := initRun(t, dir, "init")
	d := initData(env)
	if code != 0 || d == nil {
		t.Fatalf("exit %d %v", code, env)
	}
	if ch, _ := d.Get("changed"); ch != true {
		t.Error("first run not changed")
	}
	blocks, _ := d.Get("blocks")
	list, _ := blocks.([]any)
	var keys []string
	for _, b := range list {
		k, _ := b.(*jsonx.Object).Get("key")
		keys = append(keys, k.(string))
	}
	if strings.Join(keys, ",") != "skills,knowledge,milestones,decisions,map,qa" {
		t.Errorf("blocks %v", keys)
	}
	if _, ok := d.Get("instructions"); !ok {
		t.Error("no instructions")
	}
	for _, f := range []string{"AGENTS.md", "CLAUDE.md", ".rota/BACKLOG.md"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Error(err)
		}
	}
	// a second run changes nothing
	_, env, _ = initRun(t, dir, "init")
	if ch, _ := initData(env).Get("changed"); ch != false {
		t.Errorf("second run changed: %v", env)
	}
}

func TestInitNoBlocksSeedsOnly(t *testing.T) {
	dir := t.TempDir()
	code, env, _ := initRun(t, dir, "init", "--no-blocks")
	d := initData(env)
	if code != 0 {
		t.Fatal(env)
	}
	if _, ok := d.Get("blocks"); ok {
		t.Error("blocks present")
	}
	if _, ok := d.Get("instructions"); ok {
		t.Error("instructions present")
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); err == nil {
		t.Error("AGENTS.md written")
	}
}

func TestInitCorruptCountersExits70(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".rota"), 0o755)
	os.WriteFile(filepath.Join(dir, ".rota", "counters.json"), []byte("{oops"), 0o644)
	if code, _, _ := initRun(t, dir, "init", "--no-blocks"); code != 70 {
		t.Errorf("exit %d", code)
	}
}

func TestInitCheckVerb(t *testing.T) {
	dir := t.TempDir()
	code, env, _ := initRun(t, dir, "init", "check")
	d := initData(env)
	if code != 1 {
		t.Errorf("uninitialized exit %d", code)
	}
	if d == nil {
		t.Fatalf("no data on failure: %v", env)
	}
	if v, _ := d.Get("initialized"); v != false {
		t.Errorf("%v", d)
	}
	initRun(t, dir, "init", "--no-blocks")
	code, env, _ = initRun(t, dir, "init", "check")
	if v, _ := initData(env).Get("initialized"); code != 0 || v != true {
		t.Errorf("exit %d %v", code, env)
	}
}

func TestInitCheckDoesNotWalkUp(t *testing.T) {
	root := t.TempDir()
	initRun(t, root, "init", "--no-blocks")
	sub := filepath.Join(root, "sub")
	os.MkdirAll(sub, 0o755)
	if code, _, _ := initRun(t, sub, "init", "check"); code != 1 {
		t.Errorf("walked up: exit %d", code)
	}
}

func TestInitCheckWarnsOnVersionDrift(t *testing.T) {
	dir := t.TempDir()
	initRun(t, dir, "init", "--no-blocks")
	d := testDeps()
	d.InstalledVersion = func() string { return "5.0.0" }
	os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(`{"rota":{"version":"4.9.0"}}`), 0o644)
	code, env, errOut := initRunWith(t, d, dir, "init", "check")
	w, _ := env.Get("warnings")
	if l, _ := w.([]any); code != 0 || len(l) != 1 || !strings.Contains(l[0].(string), "project at 4.9.0, binary at 5.0.0") || !strings.Contains(errOut, "drift") {
		t.Errorf("exit %d warnings %v stderr %q", code, w, errOut)
	}
}

// initCode runs rota with -C and returns the exit code, restoring the cwd.
func initCode(t *testing.T, args ...string) int {
	t.Helper()
	wd, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(wd) })
	code, _, _ := runMain(args...)
	return code
}

func TestInitRemovedCodexFlagsExitUsage(t *testing.T) {
	for _, args := range [][]string{{"--codex"}, {"--skills-dir", "/x"}, {"--codex", "--skills-dir", "/x"}} {
		dir := t.TempDir()
		code := initCode(t, append([]string{"-C", dir, "init"}, args...)...)
		if code != 2 {
			t.Errorf("%v: exit %d", args, code)
		}
		if ents, _ := os.ReadDir(dir); len(ents) != 0 {
			t.Errorf("%v: wrote %v", args, ents)
		}
	}
}

func TestInitDefaultHasNoCodex(t *testing.T) {
	dir := t.TempDir()
	_, env, _ := initRun(t, dir, "init", "--no-blocks")
	if _, ok := initData(env).Get("codex"); ok {
		t.Error("codex in data")
	}
	if _, err := os.Lstat(filepath.Join(dir, ".agents")); err == nil {
		t.Error(".agents created")
	}
	gi, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if strings.Contains(string(gi), ".agents") {
		t.Error("gitignore mentions .agents")
	}
}

func TestInitFillsConfigAndStampsVersion(t *testing.T) {
	cfgPath := func(dir string) string { return filepath.Join(dir, ".rota", "config.json") }
	check := func(t *testing.T, deps *Deps, dir string) string {
		t.Helper()
		_, env, _ := initRunWith(t, deps, dir, "config", "check")
		s, _ := initData(env).Get("status")
		st, _ := s.(string)
		return st
	}
	cases := []struct {
		name    string
		version string
		seed    string // config.json before the first init ("" = none)
		wantCfg map[string]any
		stamped string
	}{
		{"fresh", "5.0.0", "", map[string]any{"rota.version": "5.0.0"}, "5.0.0"},
		{"custom value kept", "5.0.0", `{"docs":{"path":"mydocs"},"rota":{"version":"4.9.0"}}`,
			map[string]any{"docs.path": "mydocs", "rota.version": "5.0.0"}, "5.0.0"},
		{"legacy key migrated", "5.0.0", `{"hv":{"version":"4.9.0"}}`,
			map[string]any{"rota.version": "5.0.0", "hv.version": nil}, "5.0.0"},
		{"dev binary stamps nothing", "", "", map[string]any{"rota.version": ""}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := testDeps()
			deps.InstalledVersion = func() string { return tc.version }
			dir := t.TempDir()
			if tc.seed != "" {
				os.MkdirAll(filepath.Join(dir, ".rota"), 0o755)
				os.WriteFile(cfgPath(dir), []byte(tc.seed), 0o644)
			}
			code, env, _ := initRunWith(t, deps, dir, "init", "--no-blocks")
			d := initData(env)
			if code != 0 {
				t.Fatal(env)
			}
			if v, _ := d.Get("versionStamped"); v != tc.stamped {
				t.Errorf("versionStamped %v, want %q", v, tc.stamped)
			}
			f, _ := d.Get("configFilled")
			if l, _ := f.([]any); len(l) == 0 {
				t.Error("nothing filled")
			}
			if st := check(t, deps, dir); st != "upToDate" {
				t.Errorf("config check %q after init", st)
			}
			cfg := readCfg(t, cfgPath(dir))
			for k, want := range tc.wantCfg {
				if got, _ := lookupDotted(cfg, k); got != want {
					t.Errorf("%s = %v, want %v", k, got, want)
				}
			}
			// second run: nothing to fill or stamp, file untouched
			before, _ := os.ReadFile(cfgPath(dir))
			_, env, _ = initRunWith(t, deps, dir, "init", "--no-blocks")
			d = initData(env)
			f, _ = d.Get("configFilled")
			if l, _ := f.([]any); len(l) != 0 {
				t.Errorf("second run filled %v", l)
			}
			if v, _ := d.Get("versionStamped"); v != "" {
				t.Errorf("second run stamped %v", v)
			}
			if ch, _ := d.Get("changed"); ch != false {
				t.Errorf("second run changed: %v", env)
			}
			if after, _ := os.ReadFile(cfgPath(dir)); string(after) != string(before) {
				t.Error("second run rewrote config.json")
			}
		})
	}
}

func TestInitClearsVersionDrift(t *testing.T) {
	d := testDeps()
	d.InstalledVersion = func() string { return "5.0.0" }
	dir := t.TempDir()
	initRunWith(t, d, dir, "init", "--no-blocks")
	os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(`{"rota":{"version":"4.9.0"}}`), 0o644)
	initRunWith(t, d, dir, "init", "--no-blocks")
	_, env, _ := initRunWith(t, d, dir, "init", "check")
	if w, _ := env.Get("warnings"); w != nil {
		t.Errorf("drift not cleared: %v", w)
	}
}

func TestInitUmbrellaEnablesUmbrella(t *testing.T) {
	deps := testDeps()
	deps.InstalledVersion = func() string { return "5.0.0" }
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "api", ".git"), 0o755)
	code, env, _ := initRunWith(t, deps, dir, "init", "umbrella", "--all")
	d := initData(env)
	if code != 0 {
		t.Fatal(env)
	}
	if v, _ := d.Get("umbrellaEnabled"); v != true {
		t.Errorf("umbrellaEnabled %v", v)
	}
	cfg := readCfg(t, filepath.Join(dir, ".rota", "config.json"))
	if got, _ := lookupDotted(cfg, "umbrella.enabled"); got != true {
		t.Errorf("umbrella.enabled = %v", got)
	}
	_, env, _ = initRunWith(t, deps, dir, "init", "umbrella", "--all")
	if v, _ := initData(env).Get("umbrellaEnabled"); v != false {
		t.Errorf("second run umbrellaEnabled %v", v)
	}
}

func lookupDotted(cfg any, key string) (any, bool) { return config.Lookup(cfg, key) }

func readCfg(t *testing.T, path string) any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsonx.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
