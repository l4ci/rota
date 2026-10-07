package tui

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// fakeTerm scripts the terminal: each chunk is one Read, as a real terminal
// delivers one key press (or escape sequence) per read. After the script the
// input is at EOF. It is safe for the signal and resize tests, where the
// driver's goroutine writes while the test reads.
type fakeTerm struct {
	mu       sync.Mutex
	chunks   [][]byte
	out      bytes.Buffer
	raws     int
	restores int
	rawErr   error
}

func script(chunks ...string) *fakeTerm {
	t := &fakeTerm{}
	for _, c := range chunks {
		t.chunks = append(t.chunks, []byte(c))
	}
	return t
}

func (t *fakeTerm) Read(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.chunks) == 0 {
		return 0, io.EOF
	}
	n := copy(p, t.chunks[0])
	t.chunks = t.chunks[1:]
	return n, nil
}

func (t *fakeTerm) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.out.Write(p)
}

func (t *fakeTerm) makeRaw() (func(), error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.rawErr != nil {
		return nil, t.rawErr
	}
	t.raws++
	return func() { t.mu.Lock(); t.restores++; t.mu.Unlock() }, nil
}

// counts is raws and restores so far.
func (t *fakeTerm) counts() (raws, restores int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.raws, t.restores
}

func (t *fakeTerm) output() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.out.String()
}

func (t *fakeTerm) frames() []string {
	return strings.Split(t.output(), ClearScreen)[1:]
}

// term is a Terminal on the fake: 80x24, private signal channels (so nothing
// listens on the process), Exit that does nothing.
func (t *fakeTerm) term() Terminal {
	return Terminal{
		In: t, Out: t, MakeRaw: t.makeRaw,
		Size:    func() (int, int) { return 80, 24 },
		Signals: make(chan os.Signal), Resize: make(chan os.Signal),
		Exit: func(int) {},
	}
}

// rec logs every Msg a tm receives and answers through on.
type rec struct {
	mu   sync.Mutex
	msgs []Msg
	on   func(Msg) Cmd
}

func (r *rec) add(m Msg) Cmd {
	r.mu.Lock()
	r.msgs = append(r.msgs, m)
	r.mu.Unlock()
	if r.on == nil {
		return Cmd{}
	}
	return r.on(m)
}

// trace renders the log: runes as themselves, T for Tick, D for Done (D!err
// when it failed), <kind> for other keys.
func (r *rec) trace() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var b strings.Builder
	for _, m := range r.msgs {
		switch m := m.(type) {
		case Key:
			if m.Kind == KeyRune {
				b.WriteRune(m.R)
			} else {
				fmt.Fprintf(&b, "<%d>", m.Kind)
			}
		case Tick:
			b.WriteString("T")
		case Done:
			b.WriteString("D")
			if m.Err != nil {
				b.WriteString("!" + m.Err.Error())
			}
		}
	}
	return b.String()
}

// tm is a test Model: it counts keys and draws "n=<keys> <w>x<h>".
type tm struct {
	r *rec
	n int
}

func (m tm) Update(msg Msg) (Model, Cmd) {
	if _, ok := msg.(Key); ok {
		m.n++
	}
	return m, m.r.add(msg)
}

func (m tm) Render(w, h int, _ Style) string { return fmt.Sprintf("n=%d %dx%d", m.n, w, h) }

// byRune answers the keys named in cmds and every other message with no Cmd.
func byRune(cmds map[rune]Cmd) func(Msg) Cmd {
	return func(m Msg) Cmd {
		if k, ok := m.(Key); ok && k.Kind == KeyRune {
			return cmds[k.R]
		}
		return Cmd{}
	}
}

var quit = Cmd{Quit: true}

func run(t *testing.T, ft *fakeTerm, on func(Msg) Cmd) (*rec, error) {
	t.Helper()
	r := &rec{on: on}
	return r, Run(ft.term(), tm{r: r})
}

func checkRestored(t *testing.T, name string, ft *fakeTerm, raws int) {
	t.Helper()
	r, s := ft.counts()
	if r != raws || s != raws {
		t.Errorf("%s: raw %d restore %d, want %d each", name, r, s, raws)
	}
	if !strings.Contains(ft.output(), ShowCursor) {
		t.Errorf("%s: cursor left hidden: %q", name, ft.output())
	}
}

func TestRunKeysInOrderOneFramePerRead(t *testing.T) {
	ft := script("a", "bc", "\x1b[A")
	r, err := run(t, ft, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.trace(); got != "abc<1>" {
		t.Errorf("trace %q", got)
	}
	want := []string{"n=0 80x24", "n=1 80x24", "n=3 80x24", "n=4 80x24"}
	got := ft.frames()
	if len(got) != len(want) {
		t.Fatalf("frames %q, want %q", got, want)
	}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Errorf("frame %d = %q, want %q", i, got[i], want[i])
		}
	}
	if !strings.HasPrefix(ft.output(), HideCursor) {
		t.Errorf("cursor not hidden first: %q", ft.output())
	}
}

func TestRunCmdMidChunkDropsTheRest(t *testing.T) {
	var ran int
	ft := script("abc", "d")
	r, err := run(t, ft, byRune(map[rune]Cmd{'b': {Exec: func() error { ran++; return nil }}}))
	if err != nil {
		t.Fatal(err)
	}
	if got := r.trace(); got != "abDd" || ran != 1 {
		t.Errorf("trace %q ran %d, want abDd 1 (c dropped)", got, ran)
	}
}

func TestRunQuitAndEOFReturnNilAndRestore(t *testing.T) {
	ft := script("aq", "never")
	r, err := run(t, ft, byRune(map[rune]Cmd{'q': quit}))
	if err != nil || r.trace() != "aq" {
		t.Errorf("quit: err %v trace %q", err, r.trace())
	}
	checkRestored(t, "quit", ft, 1)

	ft = script("a")
	if _, err := run(t, ft, nil); err != nil {
		t.Errorf("eof: %v", err)
	}
	checkRestored(t, "eof", ft, 1)
}

func TestRunExecWithoutCookedRunsRaw(t *testing.T) {
	boom := errors.New("boom")
	ft := script("x", "q")
	var raws, restores int
	r, err := run(t, ft, byRune(map[rune]Cmd{
		'x': {Exec: func() error { raws, restores = ft.counts(); return boom }},
		'q': quit,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if raws != 1 || restores != 0 {
		t.Errorf("during Exec: raw %d restore %d, want raw 1 restore 0", raws, restores)
	}
	if got := r.trace(); got != "xD!boomq" {
		t.Errorf("trace %q", got)
	}
	checkRestored(t, "exec", ft, 1)

	// Exec+Quit returns Exec's error, and Update never sees a Done.
	ft = script("x", "z")
	r, err = run(t, ft, byRune(map[rune]Cmd{'x': {Exec: func() error { return boom }, Quit: true}}))
	if !errors.Is(err, boom) || r.trace() != "x" {
		t.Errorf("exec+quit: err %v trace %q", err, r.trace())
	}
	checkRestored(t, "exec+quit", ft, 1)
}

func TestRunCookedExecHandsOverTheTerminal(t *testing.T) {
	var raws, restores int
	var outDuring string
	ft := script("x", "q")
	r, err := run(t, ft, byRune(map[rune]Cmd{
		'x': {Cooked: true, Exec: func() error {
			raws, restores = ft.counts()
			outDuring = ft.output()
			return nil
		}},
		'q': quit,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if raws != 1 || restores != 1 {
		t.Errorf("during cooked Exec: raw %d restore %d, want 1 and 1", raws, restores)
	}
	if !strings.HasSuffix(outDuring, ShowCursor+ClearScreen) {
		t.Errorf("screen not cleared with the cursor shown before Exec: %q", outDuring)
	}
	if got := r.trace(); got != "xDq" {
		t.Errorf("trace %q", got)
	}
	if rr, _ := ft.counts(); rr != 2 {
		t.Errorf("raw sessions %d, want 2", rr)
	}
	checkRestored(t, "cooked", ft, 2)
	if f := ft.frames(); !strings.HasPrefix(f[len(f)-1], "n=1") {
		t.Errorf("no frame after the Exec: %q", f)
	}
}

func TestRunCookedWait(t *testing.T) {
	boom := errors.New("boom")
	ft := script("x", "k", "q")
	r, err := run(t, ft, byRune(map[rune]Cmd{
		'x': {Cooked: true, Wait: true, Exec: func() error { return boom }},
		'q': quit,
	}))
	if err != nil {
		t.Fatal(err)
	}
	out := ft.output()
	i, j := strings.Index(out, "error: boom\n"), strings.Index(out, "\npress any key")
	if i < 0 || j < i {
		t.Errorf("want error line then prompt: %q", out)
	}
	// k answered the prompt, so Update saw neither it nor anything before Done.
	if got := r.trace(); got != "xD!boomq" {
		t.Errorf("trace %q, want xD!boomq", got)
	}
	checkRestored(t, "wait", ft, 2)

	// No error line when Exec succeeded.
	ft = script("x", "k", "q")
	run(t, ft, byRune(map[rune]Cmd{'x': {Cooked: true, Wait: true, Exec: func() error { return nil }}, 'q': quit}))
	if out := ft.output(); strings.Contains(out, "error:") || !strings.Contains(out, "\npress any key") {
		t.Errorf("success: %q", out)
	}

	// Without Wait there is no prompt and the next chunk is a key.
	ft = script("x", "q")
	r, _ = run(t, ft, byRune(map[rune]Cmd{'x': {Cooked: true, Exec: func() error { return boom }}, 'q': quit}))
	if out := ft.output(); strings.Contains(out, "press any key") || strings.Contains(out, "error:") || r.trace() != "xD!boomq" {
		t.Errorf("no wait: trace %q out %q", r.trace(), out)
	}
}

func TestRunEOFAtPressAnyKeyEndsNil(t *testing.T) {
	ft := script("x")
	r, err := run(t, ft, byRune(map[rune]Cmd{'x': {Cooked: true, Wait: true, Exec: func() error { return errors.New("boom") }}}))
	if err != nil {
		t.Errorf("err %v", err)
	}
	if r.trace() != "x" {
		t.Errorf("Done delivered after EOF: %q", r.trace())
	}
	checkRestored(t, "eof at prompt", ft, 2)
}

func TestRunCookedQuitReturnsExecErrorWithoutReenteringRaw(t *testing.T) {
	boom := errors.New("boom")
	ft := script("x", "z")
	r, err := run(t, ft, byRune(map[rune]Cmd{'x': {Cooked: true, Wait: true, Quit: true, Exec: func() error { return boom }}}))
	if !errors.Is(err, boom) {
		t.Errorf("err %v", err)
	}
	if r.trace() != "x" || strings.Contains(ft.output(), "press any key") || strings.Contains(ft.output(), "error:") {
		t.Errorf("trace %q out %q", r.trace(), ft.output())
	}
	checkRestored(t, "cooked+quit", ft, 1)
}

func TestRunRaisingRawFailureAfterCookedExecStops(t *testing.T) {
	// MakeRaw fails the second time: Run ends with ErrNoRaw-wrapped error.
	ft := script("x", "q")
	tc := ft.term()
	n := 0
	tc.MakeRaw = func() (func(), error) {
		if n++; n == 2 {
			return nil, errors.New("gone")
		}
		return ft.makeRaw()
	}
	r := &rec{on: byRune(map[rune]Cmd{'x': {Cooked: true, Exec: func() error { return nil }}, 'q': quit})}
	err := Run(tc, tm{r: r})
	if err == nil || !strings.Contains(err.Error(), "gone") {
		t.Errorf("err %v", err)
	}
	checkRestored(t, "raw lost", ft, 1)
}

func TestRunChainedCmds(t *testing.T) {
	var ran []string
	step := func(n string, err error) func() error {
		return func() error { ran = append(ran, n); return err }
	}
	e2 := errors.New("two")
	ft := script("x", "q")
	r, err := run(t, ft, func(m Msg) Cmd {
		switch m := m.(type) {
		case Key:
			if m.Is('x') {
				return Cmd{Exec: step("one", nil)}
			}
			if m.Is('q') {
				return quit
			}
		case Done:
			if len(ran) == 1 {
				return Cmd{Exec: step("two", e2)}
			}
		}
		return Cmd{}
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(ran, ",") != "one,two" || r.trace() != "xDD!twoq" {
		t.Errorf("ran %v trace %q", ran, r.trace())
	}
	checkRestored(t, "chain", ft, 1)
}

func TestRunPanicRestoresAndPropagates(t *testing.T) {
	for name, on := range map[string]func(Msg) Cmd{
		"update": func(m Msg) Cmd { panic("boom") },
		"exec":   byRune(map[rune]Cmd{'a': {Exec: func() error { panic("boom") }}}),
		"cooked": byRune(map[rune]Cmd{'a': {Cooked: true, Exec: func() error { panic("boom") }}}),
	} {
		ft := script("a")
		func() {
			defer func() {
				if recover() != "boom" {
					t.Errorf("%s: panic swallowed or changed", name)
				}
			}()
			run(t, ft, on)
		}()
		r, s := ft.counts()
		if r == 0 || r != s || !strings.Contains(ft.output(), ShowCursor) {
			t.Errorf("%s: raw %d restore %d out %q", name, r, s, ft.output())
		}
	}
}

// blockIn blocks every Read until release closes, then reports EOF.
type blockIn struct {
	waiting chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockIn() *blockIn {
	return &blockIn{waiting: make(chan struct{}), release: make(chan struct{})}
}

func (b *blockIn) Read([]byte) (int, error) {
	b.once.Do(func() { close(b.waiting) })
	<-b.release
	return 0, io.EOF
}

func TestRunSignalsRestoreThenExit(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		ft := script()
		bi := newBlockIn()
		defer close(bi.release)
		sigs := make(chan os.Signal, 1)
		exited := make(chan int, 1)
		tc := ft.term()
		tc.In, tc.Signals = bi, sigs
		tc.Exit = func(code int) { exited <- code }
		go Run(tc, tm{r: &rec{}})
		select {
		case <-bi.waiting:
		case <-time.After(2 * time.Second):
			t.Fatalf("%v: never read", sig)
		}
		sigs <- sig
		select {
		case code := <-exited:
			r, s := ft.counts()
			if code != 128+int(sig) || r != 1 || s != 1 || !strings.Contains(ft.output(), ShowCursor) {
				t.Errorf("%v: exit %d raw %d restore %d", sig, code, r, s)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%v: no exit", sig)
		}
	}
}

func TestRunResizeRedrawsAtTheNewSize(t *testing.T) {
	ft := script()
	bi := newBlockIn()
	resize := make(chan os.Signal, 1)
	var mu sync.Mutex
	w, h := 80, 24
	sized := make(chan struct{}, 8)
	tc := ft.term()
	tc.In, tc.Resize = bi, resize
	tc.Size = func() (int, int) {
		mu.Lock()
		defer mu.Unlock()
		sized <- struct{}{}
		return w, h
	}
	done := make(chan error, 1)
	go func() { done <- Run(tc, tm{r: &rec{}}) }()
	wait := func(what string) {
		t.Helper()
		select {
		case <-sized:
		case <-time.After(2 * time.Second):
			t.Fatalf("no %s", what)
		}
	}
	wait("first draw")
	<-bi.waiting
	mu.Lock()
	w, h = 100, 30
	mu.Unlock()
	resize <- syscall.SIGWINCH
	wait("redraw")
	close(bi.release) // Read ends; Run's leave waits for a redraw still holding the lock
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not end")
	}
	f := ft.frames()
	if len(f) < 2 || !strings.HasPrefix(f[0], "n=0 80x24") || !strings.HasPrefix(f[1], "n=0 100x30") {
		t.Errorf("frames %q", f)
	}
	checkRestored(t, "resize", ft, 1)
}

func TestRunSizeNilDrawsZero(t *testing.T) {
	ft := script("a")
	tc := ft.term()
	tc.Size = nil
	if err := Run(tc, tm{r: &rec{}}); err != nil {
		t.Fatal(err)
	}
	if f := ft.frames(); !strings.HasPrefix(f[0], "n=0 0x0") {
		t.Errorf("frame %q", f[0])
	}
}

func TestRunNoRawNothingDrawn(t *testing.T) {
	ft := script("a")
	tc := ft.term()
	tc.MakeRaw = nil
	if err := Run(tc, tm{r: &rec{}}); err != ErrNoRaw {
		t.Errorf("nil MakeRaw: %v", err)
	}
	ft = script("a")
	ft.rawErr = errors.New("no tty")
	r := &rec{}
	err := Run(ft.term(), tm{r: r})
	if !errors.Is(err, ErrNoRaw) || !strings.Contains(err.Error(), "no tty") {
		t.Errorf("MakeRaw error: %v", err)
	}
	if ft.output() != "" || r.trace() != "" {
		t.Errorf("drew %q, update saw %q", ft.output(), r.trace())
	}
}

// tickIn scripts a deadline-capable input: the chunk "tick" is a read that
// runs out its deadline.
type tickIn struct {
	fakeTerm
	deadlines int
}

func (t *tickIn) SetReadDeadline(time.Time) error {
	t.mu.Lock()
	t.deadlines++
	t.mu.Unlock()
	return nil
}

func (t *tickIn) Read(p []byte) (int, error) {
	t.mu.Lock()
	if len(t.chunks) > 0 && string(t.chunks[0]) == "tick" {
		t.chunks = t.chunks[1:]
		t.mu.Unlock()
		return 0, os.ErrDeadlineExceeded
	}
	t.mu.Unlock()
	return t.fakeTerm.Read(p)
}

func newTickIn(chunks ...string) *tickIn {
	t := &tickIn{fakeTerm: *script(chunks...)}
	return t
}

func TestRunTick(t *testing.T) {
	ti := newTickIn("a", "tick", "b", "tick", "tick")
	tc := ti.fakeTerm.term()
	tc.In, tc.Out, tc.MakeRaw = ti, &ti.fakeTerm, ti.makeRaw
	tc.Tick = time.Hour
	r := &rec{}
	if err := Run(tc, tm{r: r}); err != nil {
		t.Fatal(err)
	}
	if got := r.trace(); got != "aTbTT" {
		t.Errorf("trace %q", got)
	}
	if ti.deadlines == 0 {
		t.Error("SetReadDeadline never called")
	}

	// Without Tick the deadline is never armed, and a deadline error is just
	// a failed read: Run ends.
	ti = newTickIn("a", "tick", "b")
	tc = ti.fakeTerm.term()
	tc.In, tc.Out, tc.MakeRaw = ti, &ti.fakeTerm, ti.makeRaw
	r = &rec{}
	if err := Run(tc, tm{r: r}); err != nil {
		t.Fatal(err)
	}
	if ti.deadlines != 0 || r.trace() != "a" {
		t.Errorf("no Tick: deadlines %d trace %q", ti.deadlines, r.trace())
	}
}

func TestRunTickWithoutDeadlineSupportStillTakesKeys(t *testing.T) {
	ft := script("a", "b")
	tc := ft.term() // fakeTerm has no SetReadDeadline and is not an *os.File
	tc.Tick = time.Millisecond
	r := &rec{}
	if err := Run(tc, tm{r: r}); err != nil {
		t.Fatal(err)
	}
	if got := r.trace(); got != "ab" {
		t.Errorf("trace %q", got)
	}
	checkRestored(t, "tick without deadline", ft, 1)
}
