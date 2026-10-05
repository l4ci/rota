package palette

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeTerm scripts the terminal: each chunk is one Read, as a real terminal
// delivers one key press (or one escape sequence) per read. After the script
// the input is at EOF.
type fakeTerm struct {
	chunks   [][]byte
	out      bytes.Buffer
	raws     int
	restores int
	rawErr   error
}

func script(keys ...string) *fakeTerm {
	t := &fakeTerm{}
	for _, k := range keys {
		t.chunks = append(t.chunks, []byte(k))
	}
	return t
}

func (t *fakeTerm) Read(p []byte) (int, error) {
	if len(t.chunks) == 0 {
		return 0, io.EOF
	}
	n := copy(p, t.chunks[0])
	t.chunks = t.chunks[1:]
	return n, nil
}

func (t *fakeTerm) makeRaw() (func(), error) {
	if t.rawErr != nil {
		return nil, t.rawErr
	}
	t.raws++
	return func() { t.restores++ }, nil
}

func (t *fakeTerm) frames() []string {
	parts := strings.Split(t.out.String(), clearScreen)
	return parts[1:]
}

func (t *fakeTerm) last() string {
	f := t.frames()
	return f[len(f)-1]
}

const (
	up   = "\x1b[A"
	down = "\x1b[B"
	esc  = "\x1b"
	ret  = "\r"
	bs   = "\x7f"
	ctlC = "\x03"
)

type calls struct{ ran []string }

func (c *calls) entries() []Entry {
	rec := func(n string) func() error { return func() error { c.ran = append(c.ran, n); return nil } }
	return []Entry{
		{Label: "Orchestrate", Hint: "start the round", Scope: InProject, Default: true, Ends: true, Run: rec("orchestrate")},
		{Label: "Round status", Scope: InProject, Run: rec("status")},
		{Label: "Doctor", Run: rec("doctor")},
		{Label: "Setup", Scope: NoProject, Default: true, Ends: true, Run: rec("setup")},
		{Label: "Quit", Quit: true},
	}
}

func (c *calls) cfg(t *fakeTerm, project bool) Config {
	return Config{
		Entries: c.entries(), InProject: project,
		Header: func() Header { return Header{Version: "0.10.1", Dir: "~/proj", Round: "idle", Host: "solo"} },
		In:     t, Out: &t.out, MakeRaw: t.makeRaw,
		Width: func() int { return 80 },
	}
}

func runWith(t *testing.T, keys ...string) (*fakeTerm, *calls, error) {
	t.Helper()
	ft, c := script(keys...), &calls{}
	err := Run(c.cfg(ft, true))
	return ft, c, err
}

func TestEnterRunsThePreselectedEntry(t *testing.T) {
	ft, c, err := runWith(t, ret)
	if err != nil || len(c.ran) != 1 || c.ran[0] != "orchestrate" {
		t.Fatalf("err %v ran %v", err, c.ran)
	}
	if !strings.Contains(ft.frames()[0], "› 1  Orchestrate") {
		t.Errorf("Orchestrate not preselected:\n%s", ft.frames()[0])
	}
}

func TestNoProjectPreselectsSetupAndHidesProjectEntries(t *testing.T) {
	ft, c := script(ret), &calls{}
	if err := Run(c.cfg(ft, false)); err != nil || len(c.ran) != 1 || c.ran[0] != "setup" {
		t.Fatalf("err %v ran %v", err, c.ran)
	}
	f := ft.frames()[0]
	if strings.Contains(f, "Orchestrate") || strings.Contains(f, "Round status") || !strings.Contains(f, "› 2  Setup") {
		t.Errorf("frame:\n%s", f)
	}
}

func TestMoveWithArrowsAndJK(t *testing.T) {
	// Items: Orchestrate, Round status, Doctor, Quit. Selection path:
	// 1, 2, 1, 0, 3 (up wraps), 0 (down wraps).
	_, c, _ := runWith(t, down, "j", "k", up, up, down, ret)
	if len(c.ran) != 1 || c.ran[0] != "orchestrate" {
		t.Errorf("ran %v", c.ran)
	}
	ft, c, _ := runWith(t, down, ret, "x") // Round status: runs, press any key, then redraw
	if len(c.ran) != 1 || c.ran[0] != "status" {
		t.Errorf("ran %v", c.ran)
	}
	if !strings.Contains(ft.out.String(), "press any key") {
		t.Errorf("no wait after a non-ending action")
	}
}

func TestFilterNarrowsAndBackspaceWidens(t *testing.T) {
	ft, c, _ := runWith(t, "d", "o", ret) // "do" -> Doctor only
	if len(c.ran) != 1 || c.ran[0] != "doctor" {
		t.Errorf("ran %v", c.ran)
	}
	if f := ft.frames()[2]; !strings.Contains(f, "filter: do_") || strings.Contains(f, "Round status") {
		t.Errorf("frame:\n%s", f)
	}
	ft, _, _ = runWith(t, "d", "o", bs, bs)
	if f := ft.last(); !strings.Contains(f, "Round status") || strings.Contains(f, "filter:") {
		t.Errorf("backspace did not widen:\n%s", f)
	}
	// With a filter typing, q and j are letters, not commands.
	ft, c, _ = runWith(t, "d", "q")
	if len(c.ran) != 0 || !strings.Contains(ft.last(), "no match") {
		t.Errorf("q inside a filter: ran %v\n%s", c.ran, ft.last())
	}
}

func TestDigitJumpsAndRuns(t *testing.T) {
	_, c, _ := runWith(t, "3")
	if len(c.ran) != 1 || c.ran[0] != "doctor" {
		t.Errorf("ran %v", c.ran)
	}
	_, c, _ = runWith(t, "9")
	if len(c.ran) != 0 {
		t.Errorf("an unused digit ran %v", c.ran)
	}
	// A digit picks by the entry's own number, even when a filter hides it.
	_, c, _ = runWith(t, "r", "3")
	if len(c.ran) != 1 || c.ran[0] != "doctor" {
		t.Errorf("ran %v", c.ran)
	}
}

func TestEscClearsTheFilterThenQuits(t *testing.T) {
	ft, c, _ := runWith(t, "d", esc)
	if f := ft.frames()[2]; strings.Contains(f, "filter:") || !strings.Contains(f, "› 1  Orchestrate") {
		t.Errorf("Esc did not clear the filter:\n%s", f)
	}
	if len(ft.frames()) != 3 || len(c.ran) != 0 {
		t.Errorf("first Esc must not quit: %d frames", len(ft.frames()))
	}
	ft, c, _ = runWith(t, esc, ret)
	if len(ft.frames()) != 1 || len(c.ran) != 0 {
		t.Errorf("second Esc quits: frames %d ran %v", len(ft.frames()), c.ran)
	}
}

func TestQuitKeysRunNothing(t *testing.T) {
	for _, k := range []string{"q", ctlC, "4\r"} { // 4 is the Quit entry
		ft, c, err := runWith(t, k, ret)
		if err != nil || len(c.ran) != 0 {
			t.Errorf("%q: err %v ran %v", k, err, c.ran)
		}
		if ft.restores != ft.raws || ft.raws == 0 {
			t.Errorf("%q: raw %d restore %d", k, ft.raws, ft.restores)
		}
	}
	// The Quit entry through Enter.
	_, c, _ := runWith(t, down, down, down, ret)
	if len(c.ran) != 0 {
		t.Errorf("Quit entry ran %v", c.ran)
	}
}

func TestEscapeSequencesAreNotLetters(t *testing.T) {
	// Right arrow, Delete and F5 carry letters/digits that must not reach the filter.
	ft, _, _ := runWith(t, "\x1b[C", "\x1b[3~", "\x1b[15~", "\x1bOP")
	if f := ft.last(); strings.Contains(f, "filter:") || strings.Contains(f, "no match") {
		t.Errorf("sequence leaked into the filter:\n%s", f)
	}
}

func TestTerminalRestoredOnEveryExit(t *testing.T) {
	check := func(name string, ft *fakeTerm) {
		t.Helper()
		if ft.raws == 0 || ft.raws != ft.restores {
			t.Errorf("%s: raw %d, restore %d", name, ft.raws, ft.restores)
		}
		if !strings.HasSuffix(strings.TrimRight(ft.out.String(), "\n"), showCursor) && !strings.Contains(ft.out.String(), showCursor) {
			t.Errorf("%s: cursor left hidden", name)
		}
	}
	ft, _, _ := runWith(t, "q")
	check("quit", ft)
	ft, _, _ = runWith(t) // input closes
	check("eof", ft)
	ft, _, _ = runWith(t, ret) // an ending action
	check("ends", ft)
	ft, _, _ = runWith(t, "3", "x", "q") // action, wait, quit
	check("action then quit", ft)

	// The action runs on a cooked terminal: restored before Run, raw again after.
	ft, c := script("2", "x", "q"), &calls{}
	cfg := c.cfg(ft, true)
	cfg.Entries[1].Run = func() error {
		if ft.restores != ft.raws {
			t.Errorf("action ran raw: raw %d restore %d", ft.raws, ft.restores)
		}
		return nil
	}
	Run(cfg)
	check("cooked action", ft)
	if ft.raws != 2 {
		t.Errorf("raw sessions = %d, want 2", ft.raws)
	}

	// A panic in an action still restores.
	ft, c = script("2"), &calls{}
	cfg = c.cfg(ft, true)
	cfg.Entries[1].Run = func() error { panic("boom") }
	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic swallowed")
			}
		}()
		Run(cfg)
	}()
	check("panic", ft)
}

// blockingTerm never delivers a key: the palette sits in Read until a signal.
type blockingTerm struct {
	fakeTerm
	waiting chan struct{}
}

func (b *blockingTerm) Read(p []byte) (int, error) {
	close(b.waiting)
	select {}
}

func TestSignalsRestoreThenExit(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		bt := &blockingTerm{waiting: make(chan struct{})}
		sigs := make(chan os.Signal, 1)
		exited := make(chan int, 1)
		c := &calls{}
		cfg := c.cfg(&bt.fakeTerm, true)
		cfg.In, cfg.Signals = bt, sigs
		cfg.Exit = func(code int) { exited <- code }
		go Run(cfg)
		<-bt.waiting
		sigs <- sig
		select {
		case code := <-exited:
			if code != 128+int(sig) || bt.raws != 1 || bt.restores != 1 {
				t.Errorf("%v: exit %d raw %d restore %d", sig, code, bt.raws, bt.restores)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%v: no exit", sig)
		}
	}
}

func TestNumberedFallback(t *testing.T) {
	cases := []struct {
		name  string
		cfg   func(*calls, *fakeTerm) Config
		input string
		ran   string
	}{
		{"dumb", func(c *calls, ft *fakeTerm) Config { cf := c.cfg(ft, true); cf.Dumb = true; return cf }, "3\nq\n", "doctor"},
		{"stty failure", func(c *calls, ft *fakeTerm) Config { ft.rawErr = errors.New("no tty"); return c.cfg(ft, true) }, "3\nq\n", "doctor"},
		{"empty line takes the default", func(c *calls, ft *fakeTerm) Config { cf := c.cfg(ft, true); cf.Dumb = true; return cf }, "\n", "orchestrate"},
		{"by name", func(c *calls, ft *fakeTerm) Config { cf := c.cfg(ft, true); cf.Dumb = true; return cf }, "doc\n", "doctor"},
	}
	for _, tc := range cases {
		c, ft := &calls{}, &fakeTerm{}
		cfg := tc.cfg(c, ft)
		cfg.In = strings.NewReader(tc.input)
		if err := Run(cfg); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if len(c.ran) != 1 || c.ran[0] != tc.ran {
			t.Errorf("%s: ran %v", tc.name, c.ran)
		}
		out := ft.out.String()
		if !strings.Contains(out, "choice [1]: ") || strings.Contains(out, "\x1b") || ft.raws != 0 {
			t.Errorf("%s: output %q raw %d", tc.name, out, ft.raws)
		}
	}
	// Unknown input asks again; EOF quits.
	c, ft := &calls{}, &fakeTerm{}
	cfg := c.cfg(ft, true)
	cfg.Dumb, cfg.In = true, strings.NewReader("zz\n")
	Run(cfg)
	if !strings.Contains(ft.out.String(), `unknown choice "zz"`) || len(c.ran) != 0 {
		t.Errorf("out %q ran %v", ft.out.String(), c.ran)
	}
}

func TestNoColorFramesCarryNoEscapes(t *testing.T) {
	ft, _, _ := runWith(t, "q")
	if f := strings.ReplaceAll(ft.frames()[0], showCursor, ""); strings.Contains(f, "\x1b") {
		t.Errorf("colorless frame has escapes: %q", f)
	}
	c, ft2 := &calls{}, script("q")
	cfg := c.cfg(ft2, true)
	cfg.Color = true
	Run(cfg)
	if !strings.Contains(ft2.frames()[0], "\x1b[") {
		t.Errorf("color frame has no escapes")
	}
}

func TestRunTerminalHonorsNOCOLORAndDumbTerm(t *testing.T) {
	t.Setenv("TERM", "dumb")
	c, ft := &calls{}, &fakeTerm{}
	cfg := c.cfg(ft, true)
	cfg.In = strings.NewReader("\n")
	if err := RunTerminal(cfg); err != nil || len(c.ran) != 1 || ft.raws != 0 {
		t.Errorf("TERM=dumb: err %v ran %v raw %d", err, c.ran, ft.raws)
	}
	// A real-looking TERM but an input that is no terminal file: still the prompt.
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "1")
	c, ft = &calls{}, &fakeTerm{}
	cfg = c.cfg(ft, true)
	cfg.In = strings.NewReader("q\n")
	RunTerminal(cfg)
	if !strings.Contains(ft.out.String(), "choice") || strings.Contains(ft.out.String(), "\x1b") {
		t.Errorf("out %q", ft.out.String())
	}
}

func TestBannerArtFitsAndNarrowFallsBack(t *testing.T) {
	art := Banner("0.10.1", RenderOpts{Width: 80})
	if len(art) != 5 || !strings.Contains(art[4], "v0.10.1") {
		t.Fatalf("banner = %q", art)
	}
	for _, l := range art {
		if len(l) > 40 {
			t.Errorf("art row wider than 40: %q", l)
		}
	}
	for _, w := range []int{39, 20} {
		one := Banner("0.10.1", RenderOpts{Width: w})
		if len(one) != 1 || one[0] != "rota 0.10.1" {
			t.Errorf("width %d: %q", w, one)
		}
	}
	if got := Banner("dev", RenderOpts{Width: 80}); !strings.Contains(got[4], "dev") || strings.Contains(got[4], "vdev") {
		t.Errorf("dev banner = %q", got[4])
	}
}

func TestRenderHeaderAndNarrowRows(t *testing.T) {
	s := New((&calls{}).entries(), true)
	f := Render(s, Header{Version: "0.10.1", Dir: "~/proj", Round: "2 active slots", Host: "tmux"}, RenderOpts{Width: 80})
	for _, want := range []string{"v0.10.1", "~/proj  ·  2 active slots  ·  tmux", "› 1  Orchestrate", "start the round", "  2  Round status"} {
		if !strings.Contains(f, want) {
			t.Errorf("frame lacks %q:\n%s", want, f)
		}
	}
	for _, l := range strings.Split(Render(s, Header{Version: "1", Dir: strings.Repeat("d", 100)}, RenderOpts{Width: 30}), "\n") {
		if n := len([]rune(l)); n > 30 {
			t.Errorf("row of %d runes at width 30: %q", n, l)
		}
	}
}

func TestDecodeKeys(t *testing.T) {
	got := DecodeKeys([]byte("\x1b[A\x1b[Bj\r\n\x7f\x03\x1b\x1b[C\xc3\xa4"))
	want := []Key{{Kind: KeyUp}, {Kind: KeyDown}, {Kind: KeyRune, R: 'j'}, {Kind: KeyEnter}, {Kind: KeyEnter},
		{Kind: KeyBackspace}, {Kind: KeyCtrlC}, {Kind: KeyEsc}, {Kind: KeyRune, R: 'ä'}}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("key %d = %v, want %v", i, got[i], want[i])
		}
	}
	if k := DecodeKeys([]byte("\x1bOA")); len(k) != 1 || k[0].Kind != KeyUp {
		t.Errorf("SS3 up = %v", k)
	}
}

func TestRunDefaultRunsThePreselectedEntry(t *testing.T) {
	c := &calls{}
	RunDefault(Config{Entries: c.entries(), InProject: true})
	RunDefault(Config{Entries: c.entries(), InProject: false})
	if strings.Join(c.ran, ",") != "orchestrate,setup" {
		t.Errorf("ran %v", c.ran)
	}
}
