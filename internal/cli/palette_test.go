package cli

import (
	"bytes"
	"flag"
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
	passingDoctor(t, deps)
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

// The header reads only the local sources, so it must build no forge at all.
func TestPaletteHeaderBuildsNoForge(t *testing.T) {
	deps := testDeps()
	bareRig(deps)
	paletteRig(deps, "q")
	useLaunchRig(deps, nil)
	built := forgeBuilds(deps)
	dir := trackerProject(t, "")
	gitT(t, dir, "init", "-q")
	t.Setenv("HOME", dir)
	t.Setenv("TMUX", "")
	t.Setenv("HERDR_ENV", "")
	_, out, _ := bareIn(t, deps, dir)
	if *built != 0 {
		t.Errorf("palette header built %d forge(s)", *built)
	}
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
	// Config (6) opens `config show`'s view inside the palette; q leaves the
	// screen, the second q quits the palette.
	term := &scriptedKeys{[]string{"6", "q", "q"}}
	deps.IsTerminal = func(any) bool { return true }
	deps.Palette = func(cfg palette.Config) error {
		cfg.In = term
		cfg.MakeRaw = func() (func(), error) { return func() {}, nil }
		cfg.Width = func() int { return 100 }
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
	if code != 0 || len(r.execs) != 0 {
		t.Fatalf("exit %d execs %v: %s", code, r.execs, errb.String())
	}
	if out := tui.Strip(outb.String()); !strings.Contains(out, "▾ work (") || strings.Contains(out, "press any key") {
		t.Errorf("config screen not opened in place:\n%s", out)
	}
}

func TestPaletteProjectsEntryOpensTheProjectsScreen(t *testing.T) {
	deps := testDeps()
	bareRig(deps)
	projectsXDG(t)
	// Projects (5) opens `projects`'s view inside the palette; q leaves the
	// screen, the second q quits the palette.
	term := &scriptedKeys{[]string{"5", "q", "q"}}
	deps.IsTerminal = func(any) bool { return true }
	deps.Palette = func(cfg palette.Config) error {
		cfg.In = term
		cfg.MakeRaw = func() (func(), error) { return func() {}, nil }
		cfg.Width = func() int { return 100 }
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
	if code != 0 || len(r.execs) != 0 {
		t.Fatalf("exit %d execs %v: %s", code, r.execs, errb.String())
	}
	out := tui.Strip(outb.String())
	if !strings.Contains(out, "c cleanup · d remove") || strings.Contains(out, "press any key") || len(term.keys) != 0 {
		t.Errorf("projects screen not opened in place (keys left %v):\n%s", term.keys, out)
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

// toggleRig opens the palette in a layoutProject, typing keys.
func toggleRig(t *testing.T, regHost string, keys ...string) (out string, rig *layoutRig, root string) {
	t.Helper()
	root, rig, deps := layoutProject(t, regHost, []string{"ben", "dana"}, "ben", "dana")
	paletteRig(deps, keys...)
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	var outb, errb bytes.Buffer
	if code := mainWith(deps, nil, &scriptedKeys{keys}, &outb, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	return outb.String(), rig, root
}

func TestPaletteLayoutToggleSwitchesAndRelabels(t *testing.T) {
	out, rig, _ := toggleRig(t, "herdr", "3", "x", "3", "x", "q")
	if got := strings.Count(out, "View: split ⇄ [tabs]"); got < 2 {
		t.Errorf("tabs label before the switch:\n%s", out)
	}
	if !strings.Contains(out, "View: [split] ⇄ tabs") || !strings.Contains(out, "enter switches to tabs") {
		t.Errorf("label does not follow the switch:\n%s", out)
	}
	if strings.Contains(out, "Split view") || strings.Contains(out, "Tab view") {
		t.Errorf("old entries remain:\n%s", out)
	}
	if len(rig.log) == 0 {
		t.Fatal("no panes moved")
	}
	if tab := rig.panes[1].Tab; tab != "t-ben" {
		t.Errorf("split then tabs left ben in %q: %v", tab, rig.log)
	}
}

func TestPaletteLayoutToggleHidden(t *testing.T) {
	for _, h := range []string{"tmux", "solo"} {
		out, _, _ := toggleRig(t, h, "q")
		if strings.Contains(out, "View:") {
			t.Errorf("host %s shows the toggle:\n%s", h, out)
		}
	}
	// No round: the project has no worker panes.
	deps := testDeps()
	paletteRig(deps, "q")
	var outb, errb bytes.Buffer
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	os.Chdir(trackerProject(t, ""))
	mainWith(deps, nil, &scriptedKeys{[]string{"q"}}, &outb, &errb)
	if strings.Contains(outb.String(), "View:") {
		t.Errorf("no round, toggle shown:\n%s", outb.String())
	}
}

func TestPaletteEntryWithAViewOpensItInPlace(t *testing.T) {
	t.Setenv("TERM", "xterm")
	deps := testDeps()
	paletteRig(deps)
	root := &Command{Name: "rota", Subs: []*Command{{
		Name: "demo", Verb: func(*flag.FlagSet) RunFunc {
			return func(*Ctx, []string) (Result, error) { return Result{Text: "x"}, nil }
		},
		View: func(*Ctx, Result) (tui.Model, error) { return demoScreen{}, nil },
	}, {
		Name: "plain", Verb: func(*flag.FlagSet) RunFunc {
			return func(*Ctx, []string) (Result, error) { return Result{Text: "x"}, nil }
		},
	}}}
	c := &Ctx{Deps: deps, Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard}
	with := paletteVerb(c, root, "Demo", "", palette.Always, "demo")
	without := paletteVerb(c, root, "Plain", "", palette.Always, "plain")
	if with.View == nil || without.View != nil {
		t.Fatalf("view wiring: with %v without %v", with.View != nil, without.View != nil)
	}
	m, err := with.View()
	if err != nil || m == nil {
		t.Fatalf("view: %v %v", m, err)
	}
	if _, err := paletteView(c, root, []string{"plain"}); err == nil {
		t.Error("a verb without a view must refuse")
	}
}

type demoScreen struct{}

func (demoScreen) Update(tui.Msg) (tui.Model, tui.Cmd) { return demoScreen{}, tui.Cmd{} }
func (demoScreen) Render(int, int, tui.Style) string   { return "demo screen\n" }
