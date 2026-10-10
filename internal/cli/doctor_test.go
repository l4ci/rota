package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/skills"
)

// doctorFakes writes fake tools into a fresh dir and points the tool lookup at
// it, returning the dir. defaultDeps reads the env once, so only a Deps built
// after this call sees it; an earlier one takes the dir as DoctorPath.
func doctorFakes(t *testing.T, tools map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	// jq is a doctor check too; every fake PATH carries one unless a test sets its own.
	all := map[string]string{"jq": "exit 0"}
	for name, body := range tools {
		all[name] = body
	}
	for name, body := range all {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("ROTA_TEST_DOCTOR_PATH", dir)
	t.Setenv("HOME", t.TempDir()) // no skills, plugin or settings of the real user
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	return dir
}

func doctorData(t *testing.T, out string) (bool, map[string]map[string]any) {
	t.Helper()
	var env struct {
		Data struct {
			OK     bool             `json:"ok"`
			Checks []map[string]any `json:"checks"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	byName := map[string]map[string]any{}
	order := ""
	for _, c := range env.Data.Checks {
		byName[c["name"].(string)] = c
		order += c["name"].(string) + ","
	}
	// The agents line appears only in a project whose agent files are stale.
	order = strings.Replace(order, "agents,", "", 1)
	// The verify line appears only in a project with no test.full or test.e2e.
	order = strings.Replace(order, "verify,", "", 1)
	if order != "git,jq,host,tracker,accounts,hook,statusline,stop-hook,guard-hook,switch,skills,codex," {
		t.Errorf("check order %s", order)
	}
	return env.Data.OK, byName
}

func TestDoctorPassesWithoutHv(t *testing.T) {
	// no .rota/: defaults, host skipped; git is a fake that says .worktrees/ is ignored
	doctorFakes(t, map[string]string{"git": `case "$1" in remote) exit 2;; esac; exit 0`})
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	code, out, _ := rotaIn(t, dir, "doctor", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	ok, c := doctorData(t, out)
	if !ok || c["git"]["status"] != "pass" || c["host"]["status"] != "skip" || c["tracker"]["status"] != "skip" {
		t.Errorf("%v", c)
	}
	if _, has := c["git"]["hint"]; has {
		t.Error("a passing check has no hint")
	}
	var env map[string]any
	json.Unmarshal([]byte(out), &env)
	if _, has := env["data"].(map[string]any)["changed"]; has {
		t.Error("doctor is read-only: no changed")
	}
}

func TestDoctorFailsWithSameData(t *testing.T) {
	// herdr dispatch with an old herdr and no git on PATH
	doctorFakes(t, map[string]string{"herdr": `echo "herdr 0.8.2"`})
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	os.MkdirAll(filepath.Join(dir, ".rota"), 0o755)
	os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(`{"work":{"dispatch":"herdr"}}`), 0o644)
	code, out, _ := rotaIn(t, dir, "doctor", "--json")
	if code != 1 {
		t.Fatalf("exit %d, want 1: %s", code, out)
	}
	ok, c := doctorData(t, out)
	if ok || c["git"]["status"] != "fail" || c["host"]["status"] != "fail" {
		t.Fatalf("%v", c)
	}
	if c["host"]["detail"] != "herdr 0.8.2, need 0.9.x" || c["git"]["hint"] == "" {
		t.Errorf("%v", c)
	}
}

func TestDoctorRejectsArgsAndRepo(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	if code, _, _ := rotaIn(t, dir, "doctor", "extra"); code != 2 {
		t.Errorf("positional: %d", code)
	}
	if code, _, _ := rotaIn(t, dir, "doctor", "--repo", "x"); code != 2 {
		t.Errorf("--repo: %d", code)
	}
}

// The skills check skips until rota skills install ran, passes after it, and
// fails when an installed file was edited.
func TestDoctorSkillsCheck(t *testing.T) {
	doctorFakes(t, map[string]string{"git": `case "$1" in remote) exit 2;; esac; exit 0`})
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	home := os.Getenv("HOME")
	skills := func() map[string]any {
		_, out, _ := rotaIn(t, dir, "doctor", "--json")
		_, c := doctorData(t, out)
		return c["skills"]
	}
	if c := skills(); c["status"] != "skip" || !strings.Contains(c["detail"].(string), "rota skills install") {
		t.Errorf("not installed: %v", c)
	}
	if code, out, _ := rotaIn(t, dir, "skills", "install", "--agent", "claude"); code != 0 {
		t.Fatalf("install %d %s", code, out)
	}
	if c := skills(); c["status"] != "pass" {
		t.Errorf("installed: %v", c)
	}
	os.WriteFile(filepath.Join(home, ".claude", "skills", "rota-work", "SKILL.md"), []byte("mine\n"), 0o644)
	if c := skills(); c["status"] != "fail" || c["hint"] != "run: rota skills update --overwrite" {
		t.Errorf("edited: %v", c)
	}
}

// An installed rota plugin counts as a skill root: doctor passes, and skills
// status reports it as a plugin row.
func TestDoctorSkillsPlugin(t *testing.T) {
	doctorFakes(t, map[string]string{"git": `case "$1" in remote) exit 2;; esac; exit 0`})
	t.Setenv("CLAUDE_CODE_PLUGIN_CACHE_DIR", "")
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	home := os.Getenv("HOME")
	set, err := skills.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	install := filepath.Join(home, ".claude", "plugins", "cache", "rota", "rota", "0.14.0")
	write := func(p, body string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range set.Paths() {
		b, _ := set.File(p)
		write(filepath.Join(install, "skills", filepath.FromSlash(p)), string(b))
	}
	write(filepath.Join(install, ".claude-plugin", "plugin.json"), `{"version":"0.14.0"}`)
	write(filepath.Join(home, ".claude", "plugins", "installed_plugins.json"),
		`{"version":2,"plugins":{"rota@rota":[{"scope":"user","installPath":"`+install+`"}]}}`)

	_, out, _ := rotaIn(t, dir, "doctor", "--json")
	_, c := doctorData(t, out)
	if c["skills"]["status"] != "pass" {
		t.Errorf("plugin install: %v", c["skills"])
	}
	code, env, errOut := skillsRun(t, home, dir, "skills", "status")
	if code != 0 {
		t.Fatalf("status %d %s", code, errOut)
	}
	found := false
	for _, r := range skData(env)["roots"].([]any) {
		row := r.(map[string]any)
		if row["plugin"] == true {
			found = true
			if row["installed"] != true || row["current"] != true || row["version"] != "0.14.0" {
				t.Errorf("plugin row: %v", row)
			}
		} else if row["plugin"] != false {
			t.Errorf("row without plugin field: %v", row)
		}
	}
	if !found {
		t.Errorf("no plugin row: %v", env)
	}
}

// pluginsFile writes HOME's installed_plugins.json with body.
func pluginsFile(t *testing.T, body string) string {
	t.Helper()
	f := filepath.Join(os.Getenv("HOME"), ".claude", "plugins", "installed_plugins.json")
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return f
}

// An unreadable installed_plugins.json is reported as a warning by skills
// status, not swallowed.
func TestSkillsStatusPluginWarning(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads anything")
	}
	doctorFakes(t, nil)
	t.Setenv("CLAUDE_CODE_PLUGIN_CACHE_DIR", "")
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	f := pluginsFile(t, `{"plugins":{}}`)
	if err := os.Chmod(f, 0); err != nil {
		t.Fatal(err)
	}
	code, env, errOut := skillsRun(t, os.Getenv("HOME"), dir, "skills", "status")
	if code != 0 {
		t.Fatalf("status %d %s", code, errOut)
	}
	w, _ := skData(env)["warnings"].([]any)
	if len(w) != 1 || !strings.Contains(w[0].(string), f) {
		t.Errorf("warnings: %v", skData(env))
	}
}

// With only a plugin installed, skills update points at Claude Code's updater.
func TestSkillsUpdatePluginOnlyHint(t *testing.T) {
	doctorFakes(t, nil)
	t.Setenv("CLAUDE_CODE_PLUGIN_CACHE_DIR", "")
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	skillsRun(t, os.Getenv("HOME"), dir, "skills", "status") // keep HOME stable
	pluginsFile(t, `{"plugins":{"rota@rota":[{"scope":"user","installPath":"`+filepath.Join(os.Getenv("HOME"), "nowhere")+`"}]}}`)
	code, _, errOut := skillsRun(t, os.Getenv("HOME"), dir, "skills", "update")
	if code == 0 || !strings.Contains(errOut, "claude plugin update rota@rota") || strings.Contains(errOut, "rota skills install") {
		t.Errorf("plugin only: %d %s", code, errOut)
	}
	pluginsFile(t, `{"plugins":{}}`)
	code, _, errOut = skillsRun(t, os.Getenv("HOME"), dir, "skills", "update")
	if code == 0 || !strings.Contains(errOut, "rota skills install") {
		t.Errorf("nothing installed: %d %s", code, errOut)
	}
}

// The agents check warns, naming the verb, until rota agents write ran, then
// disappears.
func TestDoctorAgentsCheck(t *testing.T) {
	doctorFakes(t, map[string]string{"git": `case "$1" in remote) exit 2;; esac; exit 0`})
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	os.MkdirAll(filepath.Join(dir, ".rota"), 0o755)
	os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(`{}`), 0o644)
	_, out, _ := rotaIn(t, dir, "doctor", "--json")
	_, c := doctorData(t, out)
	if c["agents"]["status"] != "warn" || !strings.Contains(c["agents"]["hint"].(string), "rota agents write") {
		t.Fatalf("%v", c["agents"])
	}
	if code, o, e := rotaIn(t, dir, "agents", "write"); code != 0 {
		t.Fatalf("write: %d %s %s", code, o, e)
	}
	_, out, _ = rotaIn(t, dir, "doctor", "--json")
	if _, c := doctorData(t, out); c["agents"] != nil {
		t.Fatalf("still reported: %v", c["agents"])
	}
}

// The verify check warns, never fails, while test.full and test.e2e are both
// empty, and disappears once either is set (#493).
func TestDoctorVerifyCheck(t *testing.T) {
	doctorFakes(t, map[string]string{"git": `case "$1" in remote) exit 2;; esac; exit 0`})
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	if code, out, _ := rotaIn(t, dir, "init"); code != 0 {
		t.Fatalf("init %d %s", code, out)
	}
	verify := func() map[string]any {
		_, out, _ := rotaIn(t, dir, "doctor", "--json")
		_, c := doctorData(t, out)
		return c["verify"]
	}
	c := verify()
	if c["status"] != "warn" || !strings.Contains(c["hint"].(string), "rota config set test.full '[...]'") || !strings.Contains(c["detail"].(string), "merge gate will refuse") {
		t.Errorf("empty test.full: %v", c)
	}
	for _, key := range []string{"test.full", "test.e2e"} {
		rotaIn(t, dir, "config", "set", "test.full", "[]")
		rotaIn(t, dir, "config", "set", "test.e2e", "[]")
		if code, out, _ := rotaIn(t, dir, "config", "set", key, `["true"]`); code != 0 {
			t.Fatalf("set %s: %d %s", key, code, out)
		}
		if c := verify(); c != nil {
			t.Errorf("%s set still warns: %v", key, c)
		}
	}
}
