package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/hook"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/skills"
)

func TestRewriteMarkers(t *testing.T) {
	in := "<!-- hv-knowledge-start -->\nx\n<!-- hv-knowledge-end -->\n<!-- hv:decisions:start -->\n<!-- hv:decisions:end -->\n" +
		"<!-- hv-handoff: orchestrator -->\n<!-- hv:fields 1/2 -->\n<!-- hv:comment -->\n<!-- hv-skills-start -->\n<!-- rota:claim -->\nprose hv-knowledge-start\n"
	want := "<!-- rota-knowledge-start -->\nx\n<!-- rota-knowledge-end -->\n<!-- rota-decisions-start -->\n<!-- rota-decisions-end -->\n" +
		"<!-- rota-handoff: orchestrator -->\n<!-- rota:fields 1/2 -->\n<!-- rota:comment -->\n<!-- rota-skills-start -->\n<!-- rota:claim -->\nprose hv-knowledge-start\n"
	got, n := RewriteMarkers(in)
	if got != want || n != 8 {
		t.Errorf("n=%d\n%q", n, got)
	}
	if again, n := RewriteMarkers(got); again != got || n != 0 {
		t.Errorf("not idempotent: n=%d", n)
	}
}

func TestRewriteBlockHeadingsOnlyInsideBlocks(t *testing.T) {
	in := "## hv\nkeep\n<!-- rota-skills-start -->\n## hv\nbody\n## hv-skills\n<!-- rota-skills-end -->\n## hv-skills\n"
	got, n := rewriteBlockHeadings(in)
	want := "## hv\nkeep\n<!-- rota-skills-start -->\n## rota\nbody\n## rota\n<!-- rota-skills-end -->\n## hv-skills\n"
	if got != want || n != 2 {
		t.Errorf("n=%d %q", n, got)
	}
}

func TestRewriteGitignoreKeepsOrderAndOtherLines(t *testing.T) {
	in := "dist/\n# ── hv ──\n.hv/bin/\n.hv/status.json\n/.hv/x\n!.hv/keep\n.hv/**/*.lock\n.hvx\n.worktrees/\n"
	want := "dist/\n# ── rota ──\n.rota/bin/\n.rota/status.json\n/.rota/x\n!.rota/keep\n.rota/**/*.lock\n.hvx\n.worktrees/\n"
	got, n := RewriteGitignore(in)
	if got != want || n != 6 {
		t.Errorf("n=%d %q", n, got)
	}
}

func TestConvertConfigStampsVersionAndMovesKeys(t *testing.T) {
	cases := map[string]string{
		`{"hvSkills":{"version":"4.9.0"}}`:                            "4.9.0",
		`{"hv":{"version":"5.1.0","x":1},"version":"3.0.0"}`:          "5.1.0",
		`{"version":"3.4.0","work":{}}`:                               "3.4.0",
		`{"rota":{"version":"6.0.0"},"hvSkills":{"version":"4.9.0"}}`: "6.0.0",
	}
	for in, ver := range cases {
		out, ch, err := convertConfig([]byte(in), true, "")
		if err != nil || !ch {
			t.Fatalf("%s: ch=%v err=%v", in, ch, err)
		}
		v, _ := jsonx.Decode(out)
		obj := v.(*jsonx.Object)
		r, _ := getObj(obj, "rota")
		if getString(r, "version") != ver || hasKey(obj, "hv") || hasKey(obj, "hvSkills") || hasKey(obj, "version") {
			t.Errorf("%s -> %s", in, out)
		}
		if _, ch, _ := convertConfig(out, true, ""); ch {
			t.Errorf("%s: second pass changed", in)
		}
	}
	out, _, _ := convertConfig([]byte(`{"hv":{"version":"5.1.0","x":1}}`), true, "7.0.0")
	if !strings.Contains(string(out), `"x": 1`) || !strings.Contains(string(out), `"7.0.0"`) {
		t.Errorf("want installed version and moved key: %s", out)
	}
	// Without stamp the version keys stay: a killed run keeps the old one.
	out, ch, _ := convertConfig([]byte(`{"hv":{"version":"5.1.0","x":1}}`), false, "7.0.0")
	if !ch || !strings.Contains(string(out), `"5.1.0"`) || !strings.Contains(string(out), `"rota"`) {
		t.Errorf("unstamped: %s", out)
	}
}

const hvSettings = `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"hv hook stop # hv-hook"},{"type":"command","command":"keep-me"}]}],` +
	`"SessionStart":[{"matcher":"^(startup|clear)$","hooks":[{"type":"command","command":"hv hook session-start # hv-hook"}]}]},` +
	`"statusLine":{"type":"command","command":"hv statusline dump --then 'echo x'","hvWrapped":"echo x","hvWrappedFrom":"user"}}`

func TestRewriteSettingsMatchesHookInstall(t *testing.T) {
	v, _ := jsonx.Decode([]byte(hvSettings))
	o := v.(*jsonx.Object)
	if !rewriteSettings(o) {
		t.Fatal("nothing rewritten")
	}
	b, _ := jsonx.Marshal(o)
	got := string(b)
	for _, want := range []string{"rota hook stop # rota-hook", "rota hook session-start # rota-hook", "rota statusline dump --then 'echo x'", `"rotaWrapped"`, `"rotaWrappedFrom"`, "keep-me"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, "hv") {
		t.Errorf("hv left in %s", got)
	}
	if rewriteSettings(o) {
		t.Error("second rewrite changed the settings")
	}
	// What rota hook install does next only adds the prompt hook, which hv never had.
	files := map[hook.Scope]*jsonx.Object{hook.ScopeUser: o}
	out, err := hook.Install(hook.InstallIn{Scope: hook.ScopeUser, Files: files})
	if err != nil || !out.Changed || out.Blocked || out.Statusline != hook.SLKept {
		t.Errorf("install after migrate: %+v %v", out, err)
	}
	if out, err = hook.Install(hook.InstallIn{Scope: hook.ScopeUser, Files: files}); err != nil || out.Changed {
		t.Errorf("second install: %+v %v", out, err)
	}
}

func TestRewriteSettingsDropsHvHookNextToRotaOne(t *testing.T) {
	in := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"rota hook stop # rota-hook"}]},{"hooks":[{"type":"command","command":"hv hook stop # hv-hook"}]}]}}`
	v, _ := jsonx.Decode([]byte(in))
	o := v.(*jsonx.Object)
	if !rewriteSettings(o) {
		t.Fatal("not rewritten")
	}
	b, _ := jsonx.Marshal(o)
	if strings.Count(string(b), "hook stop") != 1 || strings.Contains(string(b), "hv") {
		t.Errorf("duplicate kept: %s", b)
	}
}

// ---- end to end ------------------------------------------------------------

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return gittest.Run(t, dir, args...)
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func sha(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// hvProject writes a committed hv-era project into dir.
func hvProject(t *testing.T, dir string) {
	t.Helper()
	gitIn(t, dir, "init", "-q")
	write(t, filepath.Join(dir, ".gitignore"), "# ── hv ──\n.hv/bin/\n.hv/status.json\n.worktrees/\n")
	write(t, filepath.Join(dir, "AGENTS.md"), "# P\n<!-- hv-knowledge-start -->\n## Project Knowledge\n<!-- hv-knowledge-end -->\n<!-- hv:decisions:start -->\nold\n<!-- hv:decisions:end -->\n<!-- hv-skills-start -->\n## hv\nold\n<!-- hv-skills-end -->\n")
	write(t, filepath.Join(dir, ".hv", "config.json"), `{"hvSkills":{"version":"4.9.0"}}`+"\n")
	write(t, filepath.Join(dir, ".hv", "BACKLOG.md"), "# TODO\n- Detail: `.hv/features/F1.md`\n")
	write(t, filepath.Join(dir, ".hv", "features", "F1.md"), "<!-- hv:fields 1/2 -->\nbody\n")
	write(t, filepath.Join(dir, ".hv", "handoff", "main.md"), "<!-- hv-handoff: orchestrator -->\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "init")
}

// hvSkillsRoot writes an hv install into root with one edited file, and a user
// skill beside it.
func hvSkillsRoot(t *testing.T, root string) {
	t.Helper()
	write(t, filepath.Join(root, "hv-work", "SKILL.md"), "work")
	write(t, filepath.Join(root, "hv-plan", "SKILL.md"), "edited by the user")
	write(t, filepath.Join(root, "mine", "SKILL.md"), "user skill")
	write(t, filepath.Join(root, legacyManifest), `{"schema":1,"version":"4.0.0","digest":"d","agent":"claude","files":{"hv-work/SKILL.md":"`+sha("work")+`","hv-plan/SKILL.md":"`+sha("original")+`"}}`)
}

func hvOpts(t *testing.T, dir, home string) HvOptions {
	t.Helper()
	set, err := skills.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	InstalledVersion = func() string { return "5.0.0" }
	t.Cleanup(func() { InstalledVersion = func() string { return "" } })
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	return HvOptions{Cwd: dir, Home: home, ClaudeDir: filepath.Join(home, ".claude"), Version: "5.0.0", Skills: set}
}

func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	m := map[string]string{}
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && !strings.Contains(p, "/.git/") {
			b, _ := os.ReadFile(p)
			m[p] = string(b)
		}
		return nil
	})
	return m
}

func TestRunHvDryRunWritesNothing(t *testing.T) {
	dir, home := t.TempDir(), t.TempDir()
	hvProject(t, dir)
	hvSkillsRoot(t, filepath.Join(home, ".claude", "skills"))
	write(t, filepath.Join(home, ".claude", "settings.json"), hvSettings)
	gitIn(t, dir, "branch", "hv-worker/a-1")
	before := snapshot(t, dir)
	beforeHome := snapshot(t, home)
	rep, err := RunHv(hvOpts(t, dir, home))
	if err != nil {
		t.Fatal(err)
	}
	p := rep.Projects[0]
	if rep.Noop || rep.Applied || !p.Move || !p.Blocks || !p.Stamp || len(rep.Skills) != 1 || len(rep.Settings) != 1 || len(rep.WorkerBranches) != 1 {
		t.Errorf("plan: %+v", rep)
	}
	for k, v := range snapshot(t, dir) {
		if before[k] != v {
			t.Errorf("dry-run wrote %s", k)
		}
	}
	if len(snapshot(t, dir)) != len(before) || len(snapshot(t, home)) != len(beforeHome) {
		t.Error("dry-run added or removed files")
	}
}

func TestRunHvApplyIsCompleteAndIdempotent(t *testing.T) {
	dir, home := t.TempDir(), t.TempDir()
	hvProject(t, dir)
	root := filepath.Join(home, ".claude", "skills")
	hvSkillsRoot(t, root)
	write(t, filepath.Join(home, ".claude", "settings.json"), hvSettings)
	o := hvOpts(t, dir, home)
	o.Apply = true
	rep, err := RunHv(o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".hv")); err == nil {
		t.Error(".hv/ still there")
	}
	ag := read(t, filepath.Join(dir, "AGENTS.md"))
	if strings.Contains(ag, "hv-knowledge") || strings.Contains(ag, "hv:decisions") || strings.Contains(ag, "hv-skills-") || strings.Contains(ag, "## hv\n") || !strings.Contains(ag, "<!-- rota-skills-start -->") || !strings.Contains(ag, "## rota") || !strings.Contains(ag, ".rota/KNOWLEDGE.md") {
		t.Errorf("AGENTS.md:\n%s", ag)
	}
	if g := read(t, filepath.Join(dir, ".gitignore")); g != "# ── rota ──\n.rota/bin/\n.rota/status.json\n.worktrees/\n" {
		t.Errorf(".gitignore = %q", g)
	}
	if got := read(t, filepath.Join(dir, ".rota", "BACKLOG.md")); !strings.Contains(got, "`.rota/features/F1.md`") {
		t.Errorf("BACKLOG: %q", got)
	}
	if got := read(t, filepath.Join(dir, ".rota", "features", "F1.md")); !strings.HasPrefix(got, "<!-- rota:fields 1/2 -->") {
		t.Errorf("F1: %q", got)
	}
	if got := read(t, filepath.Join(dir, ".rota", "handoff", "main.md")); !strings.HasPrefix(got, "<!-- rota-handoff: orchestrator -->") {
		t.Errorf("handoff: %q", got)
	}
	cfg := read(t, filepath.Join(dir, ".rota", "config.json"))
	if !strings.Contains(cfg, `"rota"`) || !strings.Contains(cfg, `"5.0.0"`) || strings.Contains(cfg, "hvSkills") {
		t.Errorf("config: %s", cfg)
	}
	if rep.Projects[0].Backup == "" {
		t.Error("no backup recorded")
	} else if read(t, filepath.Join(dir, rep.Projects[0].Backup, ".hv", "BACKLOG.md")) != "# TODO\n- Detail: `.hv/features/F1.md`\n" {
		t.Error("backup does not hold the original")
	}
	// Skills: managed hv files gone, edited one and user skill kept, rota installed.
	for p, want := range map[string]bool{"hv-work/SKILL.md": false, "hv-plan/SKILL.md": true, "mine/SKILL.md": true, "rota-work/SKILL.md": true, ".rota-manifest.json": true, legacyManifest: false} {
		_, err := os.Stat(filepath.Join(root, p))
		if (err == nil) != want {
			t.Errorf("%s exists=%v, want %v", p, err == nil, want)
		}
	}
	if len(rep.Skills) != 1 || rep.Skills[0].Removed != 1 || len(rep.Skills[0].Kept) != 1 || !rep.Skills[0].Reinstalled {
		t.Errorf("skills: %+v", rep.Skills)
	}
	// Settings: rota hook install only adds the prompt hook, which hv never had.
	v, _ := jsonx.Decode([]byte(read(t, filepath.Join(home, ".claude", "settings.json"))))
	files := map[hook.Scope]*jsonx.Object{hook.ScopeUser: v.(*jsonx.Object)}
	out, err := hook.Install(hook.InstallIn{Scope: hook.ScopeUser, Files: files})
	if err != nil || !out.Changed {
		t.Errorf("hook install after migrate: %+v %v", out, err)
	}
	if out, err = hook.Install(hook.InstallIn{Scope: hook.ScopeUser, Files: files}); err != nil || out.Changed {
		t.Errorf("second hook install: %+v %v", out, err)
	}

	// A second run is a noop and writes nothing, committed or not.
	before := snapshot(t, dir)
	rep2, err := RunHv(o)
	if err != nil || !rep2.Noop || rep2.Changed {
		t.Fatalf("second run: %+v %v", rep2, err)
	}
	for k, v := range snapshot(t, dir) {
		if before[k] != v {
			t.Errorf("second run wrote %s", k)
		}
	}
	if len(snapshot(t, dir)) != len(before) {
		t.Error("second run added files")
	}
}

func TestRunHvRefusals(t *testing.T) {
	blocked := func(err error, want string) {
		t.Helper()
		var r *Refusal
		if !errors.As(err, &r) || r.Blocked != want {
			t.Errorf("err = %v, want refusal %s", err, want)
		}
	}
	t.Run("dirty", func(t *testing.T) {
		dir, home := t.TempDir(), t.TempDir()
		hvProject(t, dir)
		write(t, filepath.Join(dir, "src.txt"), "x")
		_, err := RunHv(hvOpts(t, dir, home))
		blocked(err, "dirty-tree")
	})
	t.Run("both dirs", func(t *testing.T) {
		dir, home := t.TempDir(), t.TempDir()
		hvProject(t, dir)
		if err := os.Mkdir(filepath.Join(dir, ".rota"), 0o777); err != nil {
			t.Fatal(err)
		}
		_, err := RunHv(hvOpts(t, dir, home))
		blocked(err, "both-dirs")
	})
	t.Run("cwd in backup", func(t *testing.T) {
		dir, home := t.TempDir(), t.TempDir()
		hvProject(t, dir)
		bk := filepath.Join(dir, ".hv", "migrate-backup", "x")
		os.MkdirAll(bk, 0o777)
		o := hvOpts(t, dir, home)
		o.Cwd = bk
		_, err := RunHv(o)
		blocked(err, "backup-dir")
	})
	t.Run("lock held", func(t *testing.T) {
		dir, home := t.TempDir(), t.TempDir()
		hvProject(t, dir)
		lock := filepath.Join(dir, ".hv", "status.json.lock")
		f, err := os.OpenFile(lock, os.O_RDWR|os.O_CREATE, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			t.Fatal(err)
		}
		_, err = RunHv(hvOpts(t, dir, home))
		blocked(err, "lock-held")
		if _, err := os.Stat(filepath.Join(dir, ".hv")); err != nil {
			t.Error("refusal moved .hv/")
		}
	})
	t.Run("no git", func(t *testing.T) {
		dir, home := t.TempDir(), t.TempDir()
		write(t, filepath.Join(dir, ".hv", "config.json"), "{}")
		_, err := RunHv(hvOpts(t, dir, home))
		if !errors.Is(err, ErrGit) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("no state", func(t *testing.T) {
		_, err := RunHv(hvOpts(t, t.TempDir(), t.TempDir()))
		if !errors.Is(err, ErrNoState) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestRunHvResumesAfterTheMove(t *testing.T) {
	dir, home := t.TempDir(), t.TempDir()
	hvProject(t, dir)
	if err := os.Rename(filepath.Join(dir, ".hv"), filepath.Join(dir, ".rota")); err != nil {
		t.Fatal(err)
	}
	o := hvOpts(t, dir, home)
	o.Apply = true
	rep, err := RunHv(o)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Noop || rep.Projects[0].Move {
		t.Errorf("resume: %+v", rep.Projects)
	}
	if got := read(t, filepath.Join(dir, ".rota", "features", "F1.md")); !strings.HasPrefix(got, "<!-- rota:fields") {
		t.Errorf("F1 = %q", got)
	}
	if rep, err := RunHv(o); err != nil || !rep.Noop {
		t.Errorf("after resume: %+v %v", rep, err)
	}
}

func TestRunHvSkipSkillsLeavesInstallsAlone(t *testing.T) {
	dir, home := t.TempDir(), t.TempDir()
	hvProject(t, dir)
	root := filepath.Join(home, ".agents", "skills")
	hvSkillsRoot(t, root)
	o := hvOpts(t, dir, home)
	o.Apply, o.SkipSkills = true, true
	rep, err := RunHv(o)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Skills) != 0 {
		t.Errorf("skills touched: %+v", rep.Skills)
	}
	if read(t, filepath.Join(root, "hv-work", "SKILL.md")) != "work" {
		t.Error("hv skill removed")
	}
	if _, err := os.Stat(filepath.Join(root, "rota-work")); err == nil {
		t.Error("rota skills installed")
	}
}

func TestRunHvMigratesRegisteredSubRepoWithItsOwnBackup(t *testing.T) {
	dir, home := t.TempDir(), t.TempDir()
	hvProject(t, dir)
	sub := filepath.Join(dir, "web")
	os.MkdirAll(sub, 0o777)
	hvProject(t, sub)
	write(t, filepath.Join(dir, ".hv", "repos.json"), `{"repos":[{"name":"web","path":"web"}]}`)
	write(t, filepath.Join(dir, ".gitignore"), "# ── hv ──\n.hv/bin/\nweb/\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "umbrella")
	o := hvOpts(t, dir, home)
	o.Apply = true
	rep, err := RunHv(o)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Projects) != 2 || rep.Projects[0].Backup == "" || rep.Projects[1].Backup == "" || rep.Projects[0].Dir == rep.Projects[1].Dir {
		t.Fatalf("projects: %+v", rep.Projects)
	}
	for _, d := range []string{dir, sub} {
		if _, err := os.Stat(filepath.Join(d, ".hv")); err == nil {
			t.Errorf("%s still has .hv/", d)
		}
		if _, err := os.Stat(filepath.Join(d, ".rota", "config.json")); err != nil {
			t.Errorf("%s: no .rota/config.json", d)
		}
	}
}

func TestLegacyState(t *testing.T) {
	base := t.TempDir()
	os.MkdirAll(filepath.Join(base, "a", ".hv"), 0o777)
	os.MkdirAll(filepath.Join(base, "a", "b", "c"), 0o777)
	os.MkdirAll(filepath.Join(base, "m", ".hv"), 0o777)
	os.MkdirAll(filepath.Join(base, "m", ".rota"), 0o777)
	os.MkdirAll(filepath.Join(base, "m", "x"), 0o777)
	if d, ok := LegacyState(filepath.Join(base, "a", "b", "c")); !ok || d != filepath.Join(base, "a") {
		t.Errorf("legacy = %q %v", d, ok)
	}
	if _, ok := LegacyState(filepath.Join(base, "m", "x")); ok {
		t.Error("a dir with .rota/ is not legacy")
	}
	if _, ok := LegacyState(base); ok {
		t.Error("no state is not legacy")
	}
}
