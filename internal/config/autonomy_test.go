package config

import (
	"errors"
	"github.com/l4ci/rota/internal/projects"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readLocal(t *testing.T, root string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, ".rota", "config.local.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAutonomyLevelEnum(t *testing.T) {
	for _, good := range []string{"off", "roundtrip", "auto"} {
		if _, err := Set(project(t, "", ""), "autonomy.level", good); err != nil {
			t.Errorf("set %q: %v", good, err)
		}
		if got := Retired(projects.OSEnv(), project(t, `{"autonomy": {"level": "`+good+`"}}`, "")); len(got) != 0 {
			t.Errorf("check %q: %v", good, got)
		}
	}
	for _, bad := range []string{"loop", "full", "bogus"} {
		_, err := Set(project(t, "", ""), "autonomy.level", bad)
		if !errors.Is(err, ErrBadValue) || !strings.Contains(err.Error(), "off, roundtrip, auto") {
			t.Errorf("set %q: err = %v, want ErrBadValue naming the levels", bad, err)
		}
		got := Retired(projects.OSEnv(), project(t, `{"autonomy": {"level": "`+bad+`"}}`, ""))
		if len(got) != 1 || !strings.Contains(got[0], `autonomy.level "`+bad+`"`) || !strings.Contains(got[0], "roundtrip") {
			t.Errorf("check %q: %v", bad, got)
		}
	}
}

// An auto from before the three levels meant one hop: fill rewrites it to
// roundtrip once, then raises the stamp so a later, deliberate auto stays.
func TestFillRewritesAutoOnce(t *testing.T) {
	for name, stamp := range map[string]string{"old": `"rota": {"version": "0.20.0"}, `, "older": `"rota": {"version": "0.19.3"}, `, "empty": ``, "garbage": `"rota": {"version": "dev"}, `, "legacy": `"hv": {"version": "4.2.0"}, `} {
		root := project(t, `{`+stamp+`"autonomy": {"level": "auto"}}`, `{"autonomy": {"level": "auto"}}`)
		if p := AutonomyPending(projects.OSEnv(), root); !p.Rewrite || !p.LocalRewrite {
			t.Errorf("%s: pending %+v, want both files rewritten", name, p)
		}
		if _, err := Fill(projects.OSEnv(), root); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		cfg := Load(projects.OSEnv(), configPath(root))
		if v, _ := Value(Load(projects.OSEnv(), configPath(root)), "autonomy.level"); v != "roundtrip" {
			t.Errorf("%s: level %v after fill", name, v)
		}
		if !strings.Contains(read(t, root), `"roundtrip"`) || !strings.Contains(readLocal(t, root), `"roundtrip"`) {
			t.Errorf("%s: files\n%s\n%s", name, read(t, root), readLocal(t, root))
		}
		if s := StampedVersion(cfg); s != AutonomyLevelsSince {
			t.Errorf("%s: stamp %q, want %s", name, s, AutonomyLevelsSince)
		}
		// A deliberate auto after the rewrite survives every later fill.
		if _, err := Set(root, "autonomy.level", "auto"); err != nil {
			t.Fatal(err)
		}
		if p := AutonomyPending(projects.OSEnv(), root); p.Rewrite {
			t.Errorf("%s: second pending %+v", name, p)
		}
		if _, err := Fill(projects.OSEnv(), root); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(read(t, root), `"auto"`) {
			t.Errorf("%s: deliberate auto rewritten:\n%s", name, read(t, root))
		}
	}
	for _, stamp := range []string{AutonomyLevelsSince, "0.22.1", "1.0.0"} {
		root := project(t, `{"rota": {"version": "`+stamp+`"}, "autonomy": {"level": "auto"}}`, `{"autonomy": {"level": "auto"}}`)
		if p := AutonomyPending(projects.OSEnv(), root); p.Rewrite || p.LocalRewrite {
			t.Errorf("%s: pending %+v", stamp, p)
		}
		if _, err := Fill(projects.OSEnv(), root); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(read(t, root), `"auto"`) || !strings.Contains(readLocal(t, root), `"auto"`) {
			t.Errorf("%s: current auto rewritten:\n%s\n%s", stamp, read(t, root), readLocal(t, root))
		}
	}
	// An old stamp with no auto anywhere: nothing to rewrite, stamp untouched.
	root := project(t, `{"rota": {"version": "0.20.0"}, "autonomy": {"level": "off"}}`, "")
	if _, err := Fill(projects.OSEnv(), root); err != nil {
		t.Fatal(err)
	}
	if s := StampedVersion(Load(projects.OSEnv(), configPath(root))); s != "0.20.0" {
		t.Errorf("stamp raised without a rewrite: %q", s)
	}
}

func TestFillRemovesAutopilot(t *testing.T) {
	stamp := `"rota": {"version": "` + AutonomyLevelsSince + `"}, `
	for _, tc := range []struct {
		cfg, local string
		wasOn      bool
	}{
		{`{` + stamp + `"round": {"autopilot": true, "autopilotCap": 2}}`, "", true},
		{`{` + stamp + `"round": {"autopilot": false}}`, `{"round": {"autopilot": true}}`, true},
		{`{` + stamp + `"round": {"autopilot": false}}`, "", false},
	} {
		root := project(t, tc.cfg, tc.local)
		// Removed reads config.json only, so a local file never changes it.
		if want := strings.Contains(tc.cfg, "autopilot"); slicesContain(Removed(root), "round.autopilot") != want {
			t.Errorf("%s / %s: check names round.autopilot = %v, want %v: %v", tc.cfg, tc.local, !want, want, Removed(root))
		}
		if p := AutonomyPending(projects.OSEnv(), root); p.AutopilotWasOn != tc.wasOn {
			t.Errorf("%s / %s: pending %+v, want AutopilotWasOn %v", tc.cfg, tc.local, p, tc.wasOn)
		}
		if _, err := Fill(projects.OSEnv(), root); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(read(t, root), `"autopilot"`) {
			t.Errorf("config.json keeps round.autopilot:\n%s", read(t, root))
		}
		if !strings.Contains(read(t, root), `"autopilotCap"`) && strings.Contains(tc.cfg, "autopilotCap") {
			t.Errorf("round.autopilotCap went too:\n%s", read(t, root))
		}
		if tc.local != "" && strings.Contains(readLocal(t, root), `"autopilot"`) {
			t.Errorf("config.local.json keeps round.autopilot:\n%s", readLocal(t, root))
		}
		if len(Removed(root)) != 0 {
			t.Errorf("removed after fill: %v", Removed(root))
		}
	}
}

func slicesContain(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// A global auto written before the split meant one hop too. The global file
// carries no project stamp, so it gets its own: fill rewrites a global auto
// once, stamps the global file, and a later deliberate global auto survives.
// A project at the current stamp still migrates an unstamped global.
func TestFillRewritesGlobalAutoOnce(t *testing.T) {
	gdir := t.TempDir()
	env := dirEnv(t, gdir)
	gpath := filepath.Join(gdir, "config.json")
	os.WriteFile(gpath, []byte(`{"autonomy": {"level": "auto"}}`), 0o644)
	root := project(t, `{"rota": {"version": "`+AutonomyLevelsSince+`"}}`, "")
	if p := AutonomyPending(env, root); !p.GlobalRewrite || p.Rewrite {
		t.Fatalf("pending %+v, want only the global rewritten", p)
	}
	if _, err := Fill(env, root); err != nil {
		t.Fatal(err)
	}
	if v, _ := Value(Load(env, configPath(root)), "autonomy.level"); v != "roundtrip" {
		t.Errorf("level %v after fill", v)
	}
	if strings.Contains(read(t, root), `"autonomy"`) {
		t.Errorf("fill pinned the level in the project:\n%s", read(t, root))
	}
	if _, err := SetGlobal(gdir, "autonomy.level", "auto"); err != nil {
		t.Fatal(err)
	}
	if p := AutonomyPending(env, root); p.GlobalRewrite {
		t.Errorf("second pending %+v", p)
	}
	if _, err := Fill(env, root); err != nil {
		t.Fatal(err)
	}
	if v, _ := Value(Load(env, configPath(root)), "autonomy.level"); v != "auto" {
		t.Errorf("deliberate global auto rewritten: %v", v)
	}
	// An unreadable global is left alone and fill still works.
	os.WriteFile(gpath, []byte(`[1]`), 0o644)
	if p := AutonomyPending(env, root); p.GlobalRewrite {
		t.Errorf("corrupt global pending %+v", p)
	}
	if _, err := Fill(env, root); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(gpath); string(b) != `[1]` {
		t.Errorf("corrupt global rewritten: %s", b)
	}
}

func globalStamp(t *testing.T, path string) string {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(filepath.Dir(path), GlobalStampFile))
	if strings.Contains(string(readFileOr(path)), "version") {
		t.Errorf("the global config.json holds a stamp: %s", readFileOr(path))
	}
	return strings.TrimSpace(string(b))
}

func readFileOr(path string) []byte {
	b, _ := os.ReadFile(path)
	return b
}

// A global with no auto is stamped by fill, and a global auto written after
// the split (by config set or setup's save) carries the stamp, so the next
// fill keeps it.
func TestGlobalDeliberateAutoSurvivesFill(t *testing.T) {
	for name, seed := range map[string]string{"no-file": "", "off": `{"autonomy": {"level": "off"}}`, "other-keys": `{"models": {"worker": "haiku"}}`} {
		gdir := t.TempDir()
		env := dirEnv(t, gdir)
		gpath := filepath.Join(gdir, "config.json")
		if seed != "" {
			os.WriteFile(gpath, []byte(seed), 0o644)
		}
		root := project(t, `{"rota": {"version": "`+AutonomyLevelsSince+`"}}`, "")
		if _, err := Fill(env, root); err != nil {
			t.Fatal(err)
		}
		if seed != "" {
			if s := globalStamp(t, gpath); s != AutonomyLevelsSince {
				t.Errorf("%s: stale global not stamped by fill: %q", name, s)
			}
			os.Remove(filepath.Join(gdir, GlobalStampFile)) // unstamped again: the set must stamp it
		}
		if _, err := SetGlobal(gdir, "autonomy.level", "auto"); err != nil {
			t.Fatal(err)
		}
		if s := globalStamp(t, gpath); s != AutonomyLevelsSince {
			t.Errorf("%s: set --global did not stamp: %q", name, s)
		}
		if _, err := Fill(env, root); err != nil {
			t.Fatal(err)
		}
		if g, _ := LoadGlobal(gdir); g["autonomy.level"] != "auto" {
			t.Errorf("%s: deliberate global auto rewritten: %v", name, g["autonomy.level"])
		}
	}
	gdir := t.TempDir()
	if _, err := MergeGlobal(gdir, map[string]any{"autonomy.level": "auto"}); err != nil {
		t.Fatal(err)
	}
	if s := globalStamp(t, filepath.Join(gdir, "config.json")); s != AutonomyLevelsSince {
		t.Errorf("setup save did not stamp: %q", s)
	}
}

// #1488: on a config stamped before the split, config set autonomy.level auto
// is a post-split value. The set migrates the stale layers first, then raises
// the stamp, so the next fill keeps it.
func TestSetAutoOnStaleStampSurvivesFill(t *testing.T) {
	root := project(t, `{"rota": {"version": "0.20.0"}, "autonomy": {"level": "off"}}`, "")
	if _, err := Set(root, "autonomy.level", "auto"); err != nil {
		t.Fatal(err)
	}
	if _, err := Fill(projects.OSEnv(), root); err != nil {
		t.Fatal(err)
	}
	if v, _ := Value(Load(projects.OSEnv(), configPath(root)), "autonomy.level"); v != "auto" {
		t.Errorf("deliberate auto rewritten: %v", v)
	}
	if s := StampedVersion(Load(projects.OSEnv(), configPath(root))); s != AutonomyLevelsSince {
		t.Errorf("stamp %q", s)
	}
	// A local set stamps config.json too, and an old local auto is migrated
	// before a project set makes the stamp current.
	root = project(t, `{"rota": {"version": "0.20.0"}}`, `{"autonomy": {"level": "auto"}}`)
	if _, err := Set(root, "autonomy.level", "off"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readLocal(t, root), `"roundtrip"`) {
		t.Errorf("old local auto not migrated before the stamp rose:\n%s", readLocal(t, root))
	}
	root = project(t, `{"rota": {"version": "0.20.0"}}`, "")
	if _, err := SetIn(root, "autonomy.level", "auto", LayerLocal); err != nil {
		t.Fatal(err)
	}
	if _, err := Fill(projects.OSEnv(), root); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readLocal(t, root), `"auto"`) {
		t.Errorf("deliberate local auto rewritten:\n%s", readLocal(t, root))
	}
	// A refused value changes nothing.
	root = project(t, `{"rota": {"version": "0.20.0"}}`, `{"autonomy": {"level": "auto"}}`)
	if _, err := Set(root, "autonomy.level", "bogus"); err == nil {
		t.Fatal("bogus accepted")
	}
	if !strings.Contains(readLocal(t, root), `"auto"`) || StampedVersion(Load(projects.OSEnv(), configPath(root))) != "0.20.0" {
		t.Errorf("a refused set migrated:\n%s\n%s", read(t, root), readLocal(t, root))
	}
}

// A stamp is never raised over an old one-hop auto it guards: a local set on
// a stale project migrates config.json's auto too, and save-global copies a
// stale project's auto as roundtrip.
func TestStampNeverBlessesAnOldAuto(t *testing.T) {
	gdir := t.TempDir()
	env := dirEnv(t, gdir)
	root := project(t, `{"rota": {"version": "0.20.0"}, "autonomy": {"level": "auto"}}`, "")
	if _, err := SetIn(root, "autonomy.level", "off", LayerLocal); err != nil {
		t.Fatal(err)
	}
	if _, err := Fill(env, root); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(t, root), `"roundtrip"`) {
		t.Errorf("config.json's old auto stamped as current:\n%s", read(t, root))
	}

	root = project(t, `{"rota": {"version": "0.20.0"}, "autonomy": {"level": "auto"}}`, "")
	if _, _, _, err := SaveGlobal(root, gdir); err != nil {
		t.Fatal(err)
	}
	if _, err := Fill(env, root); err != nil {
		t.Fatal(err)
	}
	if g, _ := LoadGlobal(gdir); g["autonomy.level"] != "roundtrip" {
		t.Errorf("save-global copied a stale auto: %v", g["autonomy.level"])
	}
}

// #1502: the global stamp is written under config.json's lock before the value,
// so a stamp that fails leaves no auto on disk for a later fill to downgrade.
func TestGlobalStampFailureLeavesNoAuto(t *testing.T) {
	setters := map[string]func(dir string) error{
		"set": func(dir string) error {
			_, err := SetGlobal(dir, "autonomy.level", "auto")
			return err
		},
		"merge": func(dir string) error {
			_, err := MergeGlobal(dir, map[string]any{"autonomy.level": "auto"})
			return err
		},
	}
	for name, run := range setters {
		gdir := t.TempDir()
		// A directory where the stamp file belongs makes the stamp write fail.
		if err := os.Mkdir(filepath.Join(gdir, GlobalStampFile), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := run(gdir); err == nil {
			t.Fatalf("%s: stamp failure was not reported", name)
		}
		if g, _ := LoadGlobal(gdir); g["autonomy.level"] == "auto" {
			t.Errorf("%s: auto left on disk after the stamp failed", name)
		}
	}
}

// #1511: a set or save-global that rewrites an old auto says so, so the
// stamp it raises is not the only trace.
func TestAutonomyRewritesAreReported(t *testing.T) {
	root := project(t, `{"rota": {"version": "0.20.0"}, "autonomy": {"level": "auto"}}`, `{"autonomy": {"level": "auto"}}`)
	res, err := SetIn(root, "autonomy.level", "off", LayerLocal)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Migration.Rewrite || !res.Migration.LocalRewrite {
		t.Errorf("set did not report its rewrites: %+v", res.Migration)
	}
	// A current stamp rewrites nothing.
	res, err = SetIn(root, "autonomy.level", "off", LayerLocal)
	if err != nil || res.Migration != (AutonomyMigration{}) {
		t.Errorf("a current stamp reported %+v, %v", res.Migration, err)
	}

	gdir := t.TempDir()
	root = project(t, `{"rota": {"version": "0.20.0"}, "autonomy": {"level": "auto"}}`, "")
	_, _, am, err := SaveGlobal(root, gdir)
	if err != nil {
		t.Fatal(err)
	}
	if !am.GlobalRewrite {
		t.Errorf("save-global did not report its rewrite: %+v", am)
	}
	root = project(t, `{"rota": {"version": "0.21.0"}, "autonomy": {"level": "auto"}}`, "")
	if _, _, am, _ = SaveGlobal(root, gdir); am != (AutonomyMigration{}) {
		t.Errorf("a deliberate auto reported %+v", am)
	}
}

// #1524: a set that fails after the migration rewrote old autos still reports
// the rewrites, since the files changed and the stamp stays old.
func TestAutonomySetErrorKeepsMigration(t *testing.T) {
	root := project(t, `{"rota": {"version": "0.20.0"}, "autonomy": {"level": "auto"}}`, `{"autonomy": {"level": "auto"}}`)
	boom := errors.New("boom")
	res, err := autonomySet(root, "autonomy.level", func() (SetResult, error) { return SetResult{}, boom })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if !res.Migration.Rewrite || !res.Migration.LocalRewrite {
		t.Errorf("failed set dropped the rewrites: %+v", res.Migration)
	}
}
