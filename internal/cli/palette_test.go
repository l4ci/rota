package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/palette"
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

func TestPaletteConfigEntryOpensTheConfigEditor(t *testing.T) {
	deps := testDeps()
	bareRig(deps)
	// Config (8) runs `config edit` on a cooked terminal: the next reads are
	// its lines (toggle ship.review, finish); then a key dismisses the wait
	// and q quits the palette.
	raws, restores := new(int), new(int)
	term := &scriptedKeys{[]string{"8", "ship.review\n", "\n", "x", "q"}}
	deps.IsTerminal = func(any) bool { return true }
	deps.Palette = func(cfg palette.Config) error {
		cfg.In = term
		cfg.MakeRaw = func() (func(), error) { *raws++; return func() { *restores++ }, nil }
		cfg.Width = func() int { return 80 }
		return palette.Run(cfg)
	}
	r := useLaunchRig(deps, nil)
	root := trackerProject(t, "")
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	var outb, errb bytes.Buffer
	code := mainWith(deps, nil, term, &outb, &errb)
	out, errs := outb.String(), errb.String()
	if code != 0 || len(r.execs) != 0 {
		t.Fatalf("exit %d execs %v: %s", code, r.execs, errs)
	}
	if !strings.Contains(out, "changed: ship.review") || !strings.Contains(out, "press any key") {
		t.Errorf("editor result or wait missing:\n%s", out)
	}
	if b, _ := os.ReadFile(filepath.Join(root, ".rota", "config.json")); !strings.Contains(string(b), `"review": false`) {
		t.Errorf("config not written: %s", b)
	}
	if *raws != 2 || *restores != 2 {
		t.Errorf("raw %d restore %d, want 2/2", *raws, *restores)
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
