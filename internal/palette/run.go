package palette

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/tui"
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

// RunTerminal fills the terminal parts of cfg from the process through
// tui.NewTerminal: raw mode and width through stty on cfg.In, colors from
// NO_COLOR and TERM. A dumb or unknown TERM, or an input that is not a
// terminal file, takes the numbered prompt. cfg.In and cfg.Out are the
// caller's.
func RunTerminal(cfg Config) error {
	t, ok := tui.NewTerminal(cfg.In, cfg.Out)
	cfg.Color = t.Style.Color
	if !ok {
		cfg.Dumb = true
		return Run(cfg)
	}
	cfg.MakeRaw = t.MakeRaw
	cfg.Width = func() int { w, _ := t.Size(); return w }
	return Run(cfg)
}

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
	if i, ok := s.List.Selected(); ok && !s.Items[i].Quit && s.Items[i].Run != nil {
		return s.Items[i].Run()
	}
	return nil
}

// Run shows the palette until the person quits or an Ends entry has run.
// internal/tui's driver owns the terminal and restores it on every way out.
// An action runs on a cooked terminal so it can prompt; a terminal that
// cannot go raw gets the numbered prompt.
func Run(cfg Config) error {
	if cfg.Dumb || cfg.MakeRaw == nil {
		return runLines(cfg)
	}
	t := tui.Terminal{
		In: cfg.In, Out: cfg.Out, MakeRaw: cfg.MakeRaw, Style: tui.Style{Color: cfg.Color},
		Signals: cfg.Signals, Exit: cfg.Exit,
	}
	if cfg.Width != nil {
		t.Size = func() (int, int) { return cfg.Width(), 0 }
	}
	err := tui.Run(t, model{s: New(cfg.Entries, cfg.InProject), h: cfg.header(), built: new(tui.Model)})
	if errors.Is(err, tui.ErrNoRaw) {
		return runLines(cfg)
	}
	return err
}

// model is the palette as a tui screen. While child is set, the palette is
// hidden behind it: keys and ticks go to the child, and its quit comes back
// here as the palette list.
type model struct {
	s     State
	h     Header
	child tui.Model
	// opening is true between an entry's View Cmd and its Done; built carries
	// the screen across (Update is pure, the Exec builds it).
	opening bool
	built   *tui.Model
	msg     string // last View error
}

func (m model) Update(msg tui.Msg) (tui.Model, tui.Cmd) {
	if m.child != nil {
		return m.updateChild(msg)
	}
	if d, ok := msg.(tui.Done); ok {
		if m.opening && d.Err == nil && *m.built != nil {
			m.child, *m.built = *m.built, nil
		} else if d.Err != nil && m.opening {
			m.msg = d.Err.Error()
		}
		m.opening = false
		m.s = m.s.refresh()
		return m, tui.Cmd{}
	}
	k, ok := msg.(tui.Key)
	if !ok {
		return m, tui.Cmd{}
	}
	m.msg = ""
	var act Action
	m.s, act = m.s.Update(k)
	switch act.Kind {
	case Quit:
		return m, tui.Cmd{Quit: true}
	case RunItem:
		if view := act.Item.View; view != nil {
			m.opening = true
			return m, tui.Cmd{Exec: func() error {
				v, err := view()
				*m.built = v
				return err
			}}
		}
		run := act.Item.Run
		if run == nil {
			run = func() error { return nil }
		}
		ends := act.Item.Ends
		return m, tui.Cmd{Exec: run, Cooked: true, Wait: !ends, Quit: ends}
	}
	return m, tui.Cmd{}
}

// updateChild feeds the open screen. Its Quit closes it and shows the list;
// an Exec it asks for still runs, and its Done lands back in the child.
func (m model) updateChild(msg tui.Msg) (tui.Model, tui.Cmd) {
	next, cmd := m.child.Update(msg)
	m.child = next
	if cmd.Quit {
		m.child = nil
		cmd.Quit = false
		if cmd.Exec == nil {
			m.s = m.s.refresh()
		}
	}
	return m, cmd
}

func (m model) Render(w, h int, st tui.Style) string {
	if m.child != nil {
		return m.child.Render(w, h, st)
	}
	out := Render(m.s, m.h, RenderOpts{Width: w, Color: st.Color})
	if m.msg != "" {
		out += st.Dim(tui.Fit("error: "+m.msg, w)) + "\n"
	}
	return out
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
		s = s.refresh()
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
	s := newState(items)
	s.List.Filter = ans
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
