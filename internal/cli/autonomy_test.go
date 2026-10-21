package cli

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// config check names a leftover round.autopilot; config fill rewrites an old
// one-hop auto to roundtrip, says the autopilot key was on, and a second fill
// says nothing (#1101).
func TestConfigFillReportsAutonomyMigration(t *testing.T) {
	root := trackerProject(t, `{"rota": {"version": "0.20.0"}, "autonomy": {"level": "auto"}, "round": {"autopilot": true}}`+"\n")
	if _, out, _ := rotaIn(t, root, "config", "check"); !strings.Contains(out, "REMOVED: round.autopilot") {
		t.Errorf("check does not name round.autopilot:\n%s", out)
	}
	code, out, errOut := rotaIn(t, root, "config", "fill")
	for _, want := range []string{"removed: round.autopilot", "rewrote: autonomy.level auto -> roundtrip in config.json", "round.autopilot was true"} {
		if code != 0 || !strings.Contains(out, want) {
			t.Errorf("fill lacks %q: %d\n%s%s", want, code, out, errOut)
		}
	}
	if _, out, _ := rotaIn(t, root, "config", "show", "autonomy.level"); !strings.Contains(out, `"roundtrip"`) {
		t.Errorf("level after fill: %s", out)
	}
	if _, out, _ := rotaIn(t, root, "config", "fill"); strings.Contains(out, "rewrote") || strings.Contains(out, "autopilot") {
		t.Errorf("second fill reports again:\n%s", out)
	}
}

// The tick runs only under autonomy.level auto: off and roundtrip refuse with
// the hint that turns it on.
func TestRoundTickRefusesBelowAuto(t *testing.T) {
	for _, level := range []string{"off", "roundtrip"} {
		root := trackerProject(t, `{"autonomy": {"level": "`+level+`"}}`+"\n")
		code, env, _ := rotaRun(t, "--json", "-C", root, "round", "tick")
		if code != ExitRefused || !strings.Contains(fmt.Sprint(get(get(env, "error"), "hint")), "rota config set autonomy.level auto") {
			t.Errorf("%s: %d %v", level, code, env)
		}
	}
}

// An old global auto is rewritten and named in fill's notice.
func TestConfigFillReportsGlobalAutonomyRewrite(t *testing.T) {
	gdir := t.TempDir()
	t.Setenv("ROTA_CONFIG_DIR", gdir)
	write(t, filepath.Join(gdir, "config.json"), `{"autonomy": {"level": "auto"}}`)
	root := trackerProject(t, `{"rota": {"version": "0.21.0"}}`+"\n")
	code, out, errOut := rotaIn(t, root, "config", "fill")
	if code != 0 || !strings.Contains(out, "rewrote: autonomy.level auto -> roundtrip in the global config") || !strings.Contains(out, "--global") {
		t.Errorf("fill: %d\n%s%s", code, out, errOut)
	}
}
