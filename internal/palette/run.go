package palette

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// Config is what Run touches outside its own memory; tests fill it with
// fakes. Entries, InProject, In and Out are required.
type Config struct {
	Entries   []Entry
	InProject bool
	// Header is called once, when the palette opens.
	Header func() Header
	In     io.Reader
	Out    io.Writer
	// MakeRaw puts the terminal in key-at-a-time mode and returns what puts it
	// back. An error, or a nil MakeRaw, takes the numbered prompt.
	MakeRaw func() (restore func(), err error)
	Width   func() int
	Color   bool
	// Dumb takes the numbered prompt without trying raw mode.
	Dumb bool
	// Signals delivers SIGINT and SIGTERM while the terminal is raw; nil
	// listens on the process. Exit ends the process after the restore; nil is
	// os.Exit.
	Signals <-chan os.Signal
	Exit    func(code int)
}

// lockedWriter serializes the frame writes with the signal handler's.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// clearScreen starts every frame: cursor home, clear.
const clearScreen = "\x1b[H\x1b[2J"

const (
	hideCursor = "\x1b[?25l"
	showCursor = "\x1b[?25h"
)

func (c Config) header() Header {
	if c.Header == nil {
		return Header{}
	}
	return c.Header()
}

func (c Config) opts() RenderOpts {
	o := RenderOpts{Color: c.Color}
	if c.Width != nil {
		o.Width = c.Width()
	}
	return o
}

// RunDefault runs the preselected entry with no interaction: what Enter would
// do. It stands in for the palette where no person is at a terminal (tests).
func RunDefault(cfg Config) error {
	s := New(cfg.Entries, cfg.InProject)
	if s.Sel < len(s.Items) && !s.Items[s.Sel].Quit && s.Items[s.Sel].Run != nil {
		return s.Items[s.Sel].Run()
	}
	return nil
}

// Run shows the palette until the person quits or an Ends entry has run. The
// terminal is restored on every way out: a normal return, a panic (deferred),
// SIGINT and SIGTERM (restored before exiting 128+signal), and around every
// action, which runs on a cooked terminal so it can prompt.
func Run(cfg Config) (err error) {
	cfg.Out = &lockedWriter{w: cfg.Out} // the signal handler writes too
	if cfg.Dumb || cfg.MakeRaw == nil {
		return runLines(cfg)
	}
	var leave func()
	enter := func() error {
		l, err := cfg.enter()
		if err != nil {
			return err
		}
		leave = l
		return nil
	}
	defer func() {
		if leave != nil {
			leave()
		}
	}()
	if enter() != nil {
		return runLines(cfg)
	}

	s := New(cfg.Entries, cfg.InProject)
	h := cfg.header()
	buf := make([]byte, 64)
	for {
		fmt.Fprint(cfg.Out, clearScreen, Render(s, h, cfg.opts()))
		n, rerr := cfg.In.Read(buf)
		if n == 0 && rerr != nil {
			return nil
		}
		var act Action
		for _, k := range DecodeKeys(buf[:n]) {
			s, act = s.Update(k)
			if act.Kind != None {
				break
			}
		}
		switch act.Kind {
		case Quit:
			return nil
		case RunItem:
			leave()
			leave = nil
			fmt.Fprint(cfg.Out, clearScreen)
			var aerr error
			if act.Item.Run != nil {
				aerr = act.Item.Run()
			}
			if act.Item.Ends {
				return aerr
			}
			if aerr != nil {
				fmt.Fprintf(cfg.Out, "error: %v\n", aerr)
			}
			if err := enter(); err != nil {
				return err
			}
			fmt.Fprint(cfg.Out, "\npress any key")
			if n, _ := cfg.In.Read(buf); n == 0 {
				return nil
			}
		}
	}
}

// enter sets raw mode, hides the cursor and arms the signal handler. The
// returned leave undoes all of it, once.
func (c Config) enter() (func(), error) {
	restore, err := c.MakeRaw()
	if err != nil {
		return nil, err
	}
	fmt.Fprint(c.Out, hideCursor)
	sigs, stopSigs := c.Signals, func() {}
	if sigs == nil {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
		sigs, stopSigs = ch, func() { signal.Stop(ch) }
	}
	exit := c.Exit
	if exit == nil {
		exit = os.Exit
	}
	var once sync.Once
	done := make(chan struct{})
	leave := func() {
		once.Do(func() {
			close(done)
			stopSigs()
			fmt.Fprint(c.Out, showCursor)
			restore()
		})
	}
	go func() {
		select {
		case sig := <-sigs:
			leave()
			code := 130
			if s, ok := sig.(syscall.Signal); ok {
				code = 128 + int(s)
			}
			exit(code)
		case <-done:
		}
	}()
	return leave, nil
}

// runLines is the numbered prompt for a terminal that cannot do raw mode.
func runLines(cfg Config) error {
	s := New(cfg.Entries, cfg.InProject)
	in := bufio.NewReader(cfg.In)
	def := s.defaultSel()
	for {
		o := cfg.opts()
		fmt.Fprintln(cfg.Out, strings.Join(Banner(cfg.header().Version, o), "\n"))
		if c := cfg.header().Context(); c != "" {
			fmt.Fprintln(cfg.Out, c)
		}
		fmt.Fprintln(cfg.Out)
		for _, it := range s.Items {
			line := fmt.Sprintf("  %d  %s", it.N, it.Label)
			if it.Hint != "" {
				line += "  - " + it.Hint
			}
			fmt.Fprintln(cfg.Out, line)
		}
		if len(s.Items) == 0 {
			return nil
		}
		fmt.Fprintf(cfg.Out, "choice [%d]: ", s.Items[def].N)
		line, rerr := in.ReadString('\n')
		ans := strings.TrimSpace(line)
		if ans == "" && rerr != nil {
			return nil
		}
		it, ok := s.Items[def], true
		switch {
		case ans == "":
		case ans == "q" || ans == "quit":
			return nil
		default:
			it, ok = pick(s.Items, ans)
		}
		if !ok {
			fmt.Fprintf(cfg.Out, "unknown choice %q\n\n", ans)
			continue
		}
		if it.Quit {
			return nil
		}
		var aerr error
		if it.Run != nil {
			aerr = it.Run()
		}
		if it.Ends {
			return aerr
		}
		if aerr != nil {
			fmt.Fprintf(cfg.Out, "error: %v\n", aerr)
		}
		fmt.Fprintln(cfg.Out)
		if rerr != nil {
			return nil
		}
	}
}

// pick resolves a number or a label (exact, then the one entry containing it).
func pick(items []Item, ans string) (Item, bool) {
	if n, err := strconv.Atoi(ans); err == nil {
		for _, it := range items {
			if it.N == n {
				return it, true
			}
		}
		return Item{}, false
	}
	s := State{Items: items, Filter: ans}
	m := s.Matches()
	for _, it := range m {
		if strings.EqualFold(it.Label, ans) {
			return it, true
		}
	}
	if len(m) == 1 {
		return m[0], true
	}
	return Item{}, false
}
