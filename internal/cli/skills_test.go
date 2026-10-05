package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// skillsRun runs rota in dir with HOME pointed at home and CLAUDE_CONFIG_DIR unset.
func skillsRun(t *testing.T, home, dir string, args ...string) (int, map[string]any, string) {
	t.Helper()
	return skillsRunCfg(t, home, "", dir, args...)
}

// skillsRunCfg also sets CLAUDE_CONFIG_DIR to cfg ("" leaves it unset).
func skillsRunCfg(t *testing.T, home, cfg, dir string, args ...string) (int, map[string]any, string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	code, out, errOut := runMain(append(args, "--json")...)
	var env map[string]any
	if strings.TrimSpace(out) != "" {
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("%v: %q", err, out)
		}
	}
	return code, env, errOut
}

func skData(env map[string]any) map[string]any {
	d, _ := env["data"].(map[string]any)
	return d
}

func TestSkillsInstallStatusUpdateUninstall(t *testing.T) {
	home, work := t.TempDir(), t.TempDir()
	code, env, errOut := skillsRun(t, home, work, "skills", "install")
	if code != 0 {
		t.Fatalf("install %d %s", code, errOut)
	}
	d := skData(env)
	roots := d["roots"].([]any)
	if len(roots) != 2 || d["changed"] != true {
		t.Fatalf("%v", d)
	}
	r0 := roots[0].(map[string]any)
	if r0["agent"] != "claude" || r0["scope"] != "user" || r0["root"] != filepath.Join(home, ".claude", "skills") {
		t.Errorf("%v", r0)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "rota-pause", "SKILL.md")); err != nil {
		t.Error(err)
	}

	_, env, _ = skillsRun(t, home, work, "skills", "status")
	st := skData(env)
	for _, r := range st["roots"].([]any) {
		m := r.(map[string]any)
		if m["scope"] == "user" && (m["installed"] != true || m["current"] != true) {
			t.Errorf("status %v", m)
		}
	}

	// Idempotent: nothing changed.
	_, env, _ = skillsRun(t, home, work, "skills", "install")
	if skData(env)["changed"] != false {
		t.Error("second install changed")
	}

	// An edit survives update (exit 4) and --overwrite replaces it.
	edited := filepath.Join(home, ".claude", "skills", "rota-pause", "SKILL.md")
	os.WriteFile(edited, []byte("mine\n"), 0o644)
	code, env, _ = skillsRun(t, home, work, "skills", "update")
	if code != ExitRefused || skData(env)["blockedBy"] != "edited" {
		t.Fatalf("update %d %v", code, env)
	}
	if kept := skData(env)["kept"].([]any); len(kept) != 1 || kept[0] != edited {
		t.Errorf("kept %v", kept)
	}
	if b, _ := os.ReadFile(edited); string(b) != "mine\n" {
		t.Error("edit lost")
	}
	if code, _, _ = skillsRun(t, home, work, "skills", "update", "--overwrite"); code != 0 {
		t.Errorf("overwrite %d", code)
	}
	if b, _ := os.ReadFile(edited); string(b) == "mine\n" {
		t.Error("--overwrite kept the edit")
	}

	code, env, _ = skillsRun(t, home, work, "skills", "uninstall")
	if code != 0 || skData(env)["changed"] != true {
		t.Fatalf("uninstall %d %v", code, env)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "rota-pause")); err == nil {
		t.Error("skill left behind")
	}
	// Nothing installed any more: update fails with the install hint.
	code, env, errOut = skillsRun(t, home, work, "skills", "update")
	if code != ExitFailed || !strings.Contains(errOut, "rota skills install") {
		t.Errorf("update with nothing: %d %s", code, errOut)
	}
	if rs := skData(env)["roots"].([]any); len(rs) != 0 {
		t.Errorf("%v", rs)
	}
}

func TestSkillsAgentFlagAndUsage(t *testing.T) {
	home, work := t.TempDir(), t.TempDir()
	_, env, _ := skillsRun(t, home, work, "skills", "install", "--agent", "codex")
	if rs := skData(env)["roots"].([]any); len(rs) != 1 || rs[0].(map[string]any)["agent"] != "codex" {
		t.Errorf("%v", rs)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude")); err == nil {
		t.Error("claude root created")
	}
	for _, args := range [][]string{
		{"skills", "install", "--agent", "gemini"},
		{"skills", "install", "--scope", "everywhere"},
		{"skills", "install", "extra"},
		{"skills", "status", "--overwrite"},
	} {
		if code, _, _ := skillsRun(t, home, work, args...); code != ExitUsage {
			t.Errorf("%v: exit %d", args, code)
		}
	}
}

func TestSkillsProjectScope(t *testing.T) {
	home, plain := t.TempDir(), t.TempDir()
	if code, _, _ := skillsRun(t, home, plain, "skills", "install", "--scope", "project"); code != ExitResolution {
		t.Errorf("outside git: %d", code)
	}
	repo := t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("git init: %v %s", err, out)
	}
	sub := filepath.Join(repo, "a", "b")
	os.MkdirAll(sub, 0o755)
	code, env, errOut := skillsRun(t, home, sub, "skills", "install", "--scope", "project", "--agent", "claude")
	if code != 0 {
		t.Fatalf("%d %s", code, errOut)
	}
	root := skData(env)["roots"].([]any)[0].(map[string]any)["root"].(string)
	if real, _ := filepath.EvalSymlinks(repo); root != filepath.Join(real, ".claude", "skills") && root != filepath.Join(repo, ".claude", "skills") {
		t.Errorf("root %s", root)
	}
	if _, err := os.Stat(filepath.Join(repo, ".claude", "skills", ".rota-manifest.json")); err != nil {
		t.Error(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude")); err == nil {
		t.Error("project install touched the user root")
	}
	// Status with no scope sees the project root as installed.
	_, env, _ = skillsRun(t, home, sub, "skills", "status")
	found := false
	for _, r := range skData(env)["roots"].([]any) {
		m := r.(map[string]any)
		if m["scope"] == "project" && m["agent"] == "claude" && m["installed"] == true {
			found = true
		}
	}
	if !found {
		t.Errorf("%v", env)
	}
}

// CLAUDE_CONFIG_DIR moves the user Claude root; the Codex root stays under
// HOME.
func TestSkillsClaudeConfigDir(t *testing.T) {
	home, cfg, work := t.TempDir(), t.TempDir(), t.TempDir()
	code, _, errOut := skillsRunCfg(t, home, cfg, work, "skills", "install")
	if code != 0 {
		t.Fatalf("%d %s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(cfg, "skills", "rota-pause", "SKILL.md")); err != nil {
		t.Error(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "rota-pause", "SKILL.md")); err != nil {
		t.Error(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude")); err == nil {
		t.Error("the default Claude dir was used")
	}
}

// With no way to find the user roots the verb exits 3 instead of dropping them.
func TestSkillsNoHome(t *testing.T) {
	work := t.TempDir()
	t.Setenv("HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	old, _ := os.Getwd()
	os.Chdir(work)
	defer os.Chdir(old)
	for _, args := range [][]string{{"skills", "install"}, {"skills", "status"}, {"skills", "install", "--agent", "codex"}} {
		if code, _, errOut := runMain(args...); code != ExitResolution || !strings.Contains(errOut, "HOME") {
			t.Errorf("%v: %d %s", args, code, errOut)
		}
	}
	// A config dir alone is enough for the Claude root.
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	if code, _, errOut := runMain("skills", "install", "--agent", "claude"); code != 0 {
		t.Errorf("claude with CLAUDE_CONFIG_DIR only: %d %s", code, errOut)
	}
}

// accountProject is a rota project whose work.accounts name two existing
// config dirs and one that does not exist.
func accountProject(t *testing.T, home string) (proj, a, b, gone string) {
	t.Helper()
	proj = t.TempDir()
	a, b, gone = filepath.Join(home, ".claude-a"), filepath.Join(home, ".claude-b"), filepath.Join(home, ".claude-gone")
	for _, d := range []string{a, b, filepath.Join(proj, ".rota")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := `{"work":{"accounts":[{"name":"a","configDir":"` + a + `"},{"name":"b","configDir":"` + b + `"},{"name":"gone","configDir":"` + gone + `"},{"name":"dup","configDir":"` + filepath.Join(home, ".claude") + `/"}]}}`
	if err := os.WriteFile(filepath.Join(proj, ".rota", "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return proj, a, b, gone
}

func TestSkillsCoverWorkAccounts(t *testing.T) {
	home := t.TempDir()
	proj, a, b, gone := accountProject(t, home)
	code, env, errOut := skillsRun(t, home, proj, "skills", "install", "--agent", "claude")
	if code != 0 {
		t.Fatalf("install %d %s", code, errOut)
	}
	d := skData(env)
	var got []string
	for _, r := range d["roots"].([]any) {
		got = append(got, r.(map[string]any)["root"].(string))
	}
	want := []string{filepath.Join(home, ".claude", "skills"), filepath.Join(a, "skills"), filepath.Join(b, "skills")}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("roots %v, want %v", got, want)
	}
	if sk, _ := d["skipped"].([]any); len(sk) != 1 || sk[0] != gone {
		t.Errorf("skipped %v", d["skipped"])
	}
	if _, err := os.Stat(gone); err == nil {
		t.Error("missing config dir was created")
	}

	// One account goes stale: status flags it, update refreshes it.
	mp := filepath.Join(b, "skills", ".rota-manifest.json")
	raw, err := os.ReadFile(mp)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	m["digest"], m["version"] = "stale", "0.0.1"
	raw, _ = json.Marshal(m)
	if err := os.WriteFile(mp, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	_, env, _ = skillsRun(t, home, proj, "skills", "status", "--agent", "claude", "--scope", "user")
	current := map[string]any{}
	for _, r := range skData(env)["roots"].([]any) {
		rm := r.(map[string]any)
		current[rm["root"].(string)] = rm["current"]
	}
	if current[filepath.Join(a, "skills")] != true || current[filepath.Join(b, "skills")] != false {
		t.Errorf("status %v", current)
	}
	if code, _, errOut = skillsRun(t, home, proj, "skills", "update", "--agent", "claude"); code != 0 {
		t.Fatalf("update %d %s", code, errOut)
	}
	_, env, _ = skillsRun(t, home, proj, "skills", "status", "--agent", "claude", "--scope", "user")
	for _, r := range skData(env)["roots"].([]any) {
		if r.(map[string]any)["current"] != true {
			t.Errorf("still stale: %v", r)
		}
	}

	// doctor reads the same roots, so the lagging account would show there.
	rep := doctorSkills(home, proj)
	if rep == nil {
		t.Fatal("doctor skills nil")
	}
	n := 0
	for _, r := range rep.Roots {
		if r.Agent == "claude" && r.Scope == "user" && r.Installed {
			n++
		}
	}
	if n != 3 {
		t.Errorf("doctor sees %d installed claude user roots, want 3", n)
	}

	code, env, _ = skillsRun(t, home, proj, "skills", "uninstall", "--agent", "claude")
	if code != 0 || len(skData(env)["roots"].([]any)) != 3 {
		t.Errorf("uninstall %d %v", code, env)
	}
	if _, err := os.Stat(filepath.Join(b, "skills", "rota-pause")); err == nil {
		t.Error("account b skill left behind")
	}
}

func TestSkillsCurrentAccountFlag(t *testing.T) {
	home := t.TempDir()
	proj, a, _, _ := accountProject(t, home)
	_, env, _ := skillsRun(t, home, proj, "skills", "install", "--agent", "claude", "--current-account")
	d := skData(env)
	if rs := d["roots"].([]any); len(rs) != 1 || rs[0].(map[string]any)["root"] != filepath.Join(home, ".claude", "skills") {
		t.Errorf("%v", rs)
	}
	if _, ok := d["skipped"]; ok {
		t.Errorf("skipped reported: %v", d["skipped"])
	}
	if _, err := os.Stat(filepath.Join(a, "skills")); err == nil {
		t.Error("account dir written despite --current-account")
	}
}

func TestSkillsProjectScopeIgnoresAccounts(t *testing.T) {
	home := t.TempDir()
	proj, a, _, _ := accountProject(t, home)
	if out, err := exec.Command("git", "-C", proj, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	_, env, _ := skillsRun(t, home, proj, "skills", "install", "--agent", "claude", "--scope", "project")
	if rs := skData(env)["roots"].([]any); len(rs) != 1 {
		t.Errorf("%v", rs)
	}
	if _, err := os.Stat(filepath.Join(a, "skills")); err == nil {
		t.Error("project scope touched an account dir")
	}
}
