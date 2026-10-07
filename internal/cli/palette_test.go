package cli

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/palette"
	"github.com/l4ci/rota/internal/tui"
	"github.com/l4ci/rota/internal/version"
)

// scriptedKeys delivers one scripted key press per Read, then EOF.
type scriptedKeys struct{ keys []string }

func (s *scriptedKeys) Read(p []byte) (int, error) {
	if len(s.keys) == 0 {
		return 0, io.EOF
	}
	n := copy(p, s.keys[0])
	s.keys = s.keys[1:]
	return n, nil
}

// paletteRig fakes the terminal under the palette: the keys come from the
// script, raw mode is counted.
func paletteRig(d *Deps, keys ...string) (raws, restores *int) {
	raws, restores = new(int), new(int)
	d.IsTerminal = func(any) bool { return true }
	d.Palette = func(cfg palette.Config) error {
		cfg.In = &scriptedKeys{keys}
		cfg.MakeRaw = func() (func(), error) { *raws++; return func() { *restores++ }, nil }
		cfg.Width = func() int { return 80 }
		return palette.Run(cfg)
	}
	return raws, restores
}

func TestPaletteEnterOnTheDefaultLaunchesTheOrchestrator(t *testing.T) {
	deps := testDeps()
	setups := bareRig(deps)
	raws, restores := paletteRig(deps, "\r")
	passingDoctor(t)
	r := useLaunchRig(deps, nil)
	code, out, errs := bareIn(t, deps, trackerProject(t, ""))
	if code != 0 || *setups != 0 || len(r.execs) != 1 || r.execs[0][2] != "keepalive" {
		t.Fatalf("exit %d setups %d execs %q: %s", code, *setups, r.execs, errs)
	}
	for _, want := range []string{"› 1  Orchestrate", version.Get().Version} {
		if !strings.Contains(out, want) {
			t.Errorf("frame lacks %q:\n%s", want, out)
		}
	}
	if *raws != 1 || *restores != 1 {
		t.Errorf("terminal raw %d restore %d: must be cooked before the exec", *raws, *restores)
	}
}

func TestPaletteWithoutAProjectPreselectsSetupAndHidesProjectEntries(t *testing.T) {
	deps := testDeps()
	setups := bareRig(deps)
	paletteRig(deps, "\r")
	r := useLaunchRig(deps, nil)
	code, out, _ := bareIn(t, deps, t.TempDir())
	if code != 0 || *setups != 1 || len(r.execs) != 0 {
		t.Fatalf("exit %d setups %d execs %v", code, *setups, r.execs)
	}
	if !strings.Contains(out, "Setup") || strings.Contains(out, "Orchestrate") || strings.Contains(out, "Doctor") || !strings.Contains(out, "setup ran") {
		t.Errorf("out:\n%s", out)
	}
}

func TestPaletteContextLineShowsTildeRoundAndHost(t *testing.T) {
	deps := testDeps()
	bareRig(deps)
	paletteRig(deps, "q")
	useLaunchRig(deps, nil)
	dir := trackerProject(t, "")
	gitT(t, dir, "init", "-q")
	t.Setenv("HOME", dir)
	t.Setenv("TMUX", "")
	t.Setenv("HERDR_ENV", "")
	_, out, _ := bareIn(t, deps, dir)
	if !strings.Contains(out, "~  ·  idle  ·  ") {
		t.Errorf("context line missing:\n%s", out)
	}
}

func TestPaletteQuitRunsNothingAndExitsZero(t *testing.T) {
	for _, key := range []string{"q", "\x03", "\x1b"} {
		deps := testDeps()
		setups := bareRig(deps)
		raws, restores := paletteRig(deps, key)
		r := useLaunchRig(deps, nil)
		code, _, errs := bareIn(t, deps, trackerProject(t, ""))
		if code != 0 || *setups != 0 || len(r.execs) != 0 || *raws != *restores {
			t.Errorf("%q: exit %d setups %d execs %v raw %d/%d: %s", key, code, *setups, r.execs, *raws, *restores, errs)
		}
	}
}

func TestPaletteConfigEntryOpensTheConfigScreen(t *testing.T) {
	deps := testDeps()
	bareRig(deps)
	// Config (8) runs `config edit`, which opens the config screen in
	// process; then q quits the palette.
	var screens []tui.Model
	term := &scriptedKeys{[]string{"8", "q"}}
	deps.IsTerminal = func(any) bool { return true }
	deps.RunView = func(c *Ctx, m tui.Model) error {
		screens = append(screens, m)
		return nil
	}
	deps.Palette = func(cfg palette.Config) error {
		cfg.In = term
		cfg.MakeRaw = func() (func(), error) { return func() {}, nil }
		cfg.Width = func() int { return 80 }
		return palette.Run(cfg)
	}
	t.Setenv("TERM", "xterm")
	r := useLaunchRig(deps, nil)
	root := trackerProject(t, "")
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	var outb, errb bytes.Buffer
	code := mainWith(deps, nil, term, &outb, &errb)
	if code != 0 || len(r.execs) != 0 || len(screens) != 1 {
		t.Fatalf("exit %d execs %v screens %d: %s", code, r.execs, len(screens), errb.String())
	}
	if frame := tui.Strip(screens[0].Render(100, 30, tui.Style{})); !strings.Contains(frame, "▾ work (") {
		t.Errorf("not the config screen:\n%s", frame)
	}
}

func TestPaletteNotOpenedForPipeOrJSON(t *testing.T) {
	deps := testDeps()
	bareRig(deps)
	opened := false
	deps.Palette = func(palette.Config) error { opened = true; return nil }
	deps.IsTerminal = func(any) bool { return false }
	if code, _, _ := bareIn(t, deps, trackerProject(t, "")); code != ExitUsage || opened {
		t.Errorf("pipe: exit %d opened %v", code, opened)
	}
	deps.IsTerminal = func(any) bool { return true }
	if code, _, _ := bareIn(t, deps, trackerProject(t, ""), "--json"); code != ExitUsage || opened {
		t.Errorf("--json: exit %d opened %v", code, opened)
	}
}
