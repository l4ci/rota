package config

import (
	"errors"
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
		if got := Retired(project(t, `{"autonomy": {"level": "`+good+`"}}`, "")); len(got) != 0 {
			t.Errorf("check %q: %v", good, got)
		}
	}
	for _, bad := range []string{"loop", "full", "bogus"} {
		_, err := Set(project(t, "", ""), "autonomy.level", bad)
		if !errors.Is(err, ErrBadValue) || !strings.Contains(err.Error(), "off, roundtrip, auto") {
			t.Errorf("set %q: err = %v, want ErrBadValue naming the levels", bad, err)
		}
		got := Retired(project(t, `{"autonomy": {"level": "`+bad+`"}}`, ""))
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
		if p := AutonomyPending(root); !p.Rewrite || !p.LocalRewrite {
			t.Errorf("%s: pending %+v, want both files rewritten", name, p)
		}
		if _, err := Fill(root); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		cfg := Load(configPath(root))
		if v, _ := Value(Load(configPath(root)), "autonomy.level"); v != "roundtrip" {
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
		if p := AutonomyPending(root); p.Rewrite {
			t.Errorf("%s: second pending %+v", name, p)
		}
		if _, err := Fill(root); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(read(t, root), `"auto"`) {
			t.Errorf("%s: deliberate auto rewritten:\n%s", name, read(t, root))
		}
	}
	for _, stamp := range []string{AutonomyLevelsSince, "0.22.1", "1.0.0"} {
		root := project(t, `{"rota": {"version": "`+stamp+`"}, "autonomy": {"level": "auto"}}`, `{"autonomy": {"level": "auto"}}`)
		if p := AutonomyPending(root); p.Rewrite || p.LocalRewrite {
			t.Errorf("%s: pending %+v", stamp, p)
		}
		if _, err := Fill(root); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(read(t, root), `"auto"`) || !strings.Contains(readLocal(t, root), `"auto"`) {
			t.Errorf("%s: current auto rewritten:\n%s\n%s", stamp, read(t, root), readLocal(t, root))
		}
	}
	// An old stamp with no auto anywhere: nothing to rewrite, stamp untouched.
	root := project(t, `{"rota": {"version": "0.20.0"}, "autonomy": {"level": "off"}}`, "")
	if _, err := Fill(root); err != nil {
		t.Fatal(err)
	}
	if s := StampedVersion(Load(configPath(root))); s != "0.20.0" {
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
		if !slicesContain(Removed(root), "round.autopilot") && !strings.Contains(tc.local, "autopilot") {
			t.Errorf("%s: check does not name round.autopilot: %v", tc.cfg, Removed(root))
		}
		if p := AutonomyPending(root); p.AutopilotWasOn != tc.wasOn {
			t.Errorf("%s / %s: pending %+v, want AutopilotWasOn %v", tc.cfg, tc.local, p, tc.wasOn)
		}
		if _, err := Fill(root); err != nil {
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
	t.Setenv("ROTA_CONFIG_DIR", gdir)
	gpath := filepath.Join(gdir, "config.json")
	os.WriteFile(gpath, []byte(`{"autonomy": {"level": "auto"}}`), 0o644)
	root := project(t, `{"rota": {"version": "`+AutonomyLevelsSince+`"}}`, "")
	if p := AutonomyPending(root); !p.GlobalRewrite || p.Rewrite {
		t.Fatalf("pending %+v, want only the global rewritten", p)
	}
	if _, err := Fill(root); err != nil {
		t.Fatal(err)
	}
	if v, _ := Value(Load(configPath(root)), "autonomy.level"); v != "roundtrip" {
		t.Errorf("level %v after fill", v)
	}
	if strings.Contains(read(t, root), `"autonomy"`) {
		t.Errorf("fill pinned the level in the project:\n%s", read(t, root))
	}
	if _, err := SetGlobal(gdir, "autonomy.level", "auto"); err != nil {
		t.Fatal(err)
	}
	if p := AutonomyPending(root); p.GlobalRewrite {
		t.Errorf("second pending %+v", p)
	}
	if _, err := Fill(root); err != nil {
		t.Fatal(err)
	}
	if v, _ := Value(Load(configPath(root)), "autonomy.level"); v != "auto" {
		t.Errorf("deliberate global auto rewritten: %v", v)
	}
	// An unreadable global is left alone and fill still works.
	os.WriteFile(gpath, []byte(`[1]`), 0o644)
	if p := AutonomyPending(root); p.GlobalRewrite {
		t.Errorf("corrupt global pending %+v", p)
	}
	if _, err := Fill(root); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(gpath); string(b) != `[1]` {
		t.Errorf("corrupt global rewritten: %s", b)
	}
}
