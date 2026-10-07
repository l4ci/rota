package tui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// Msg is what Update receives: a Key, a Tick, or the Done of an Exec.
type Msg interface{ msg() }

func (Key) msg() {}

// Tick arrives every Terminal.Tick while no key does.
type Tick struct{}

func (Tick) msg() {}

// Done carries what a Cmd's Exec returned.
type Done struct{ Err error }

func (Done) msg() {}

// Model is one screen. Update is pure: it returns the next model and what
// the driver should do, and touches nothing outside. Render draws the model
// into a w x h frame (h 0 means unknown: draw it all); it styles through st
// and never writes an escape of its own.
type Model interface {
	Update(Msg) (Model, Cmd)
	Render(w, h int, st Style) string
}

// Cmd is what the driver does after an Update. The zero Cmd does nothing.
type Cmd struct {
	// Exec runs an action: a verb, a reload, another screen. Its error comes
	// back to Update as Done, unless Quit ends the screen with it.
	Exec func() error
	// Cooked hands Exec the terminal: raw mode off, the screen cleared, the
	// cursor shown, so it can print and prompt. Without it Exec runs while
	// the screen stays up and must not touch the terminal.
	Cooked bool
	// Wait, after a Cooked Exec, prints its error if any and "press any key",
	// so its output stays readable before the next frame.
	Wait bool
	// Quit leaves the screen, after Exec when there is one; Run returns
	// Exec's error.
	Quit bool
}

func (c Cmd) zero() bool { return c.Exec == nil && !c.Quit }

// Terminal is what Run touches outside its own memory; NewTerminal fills it
// from the process and tests fill it with fakes. In and Out are required.
type Terminal struct {
	In  io.Reader
	Out io.Writer
	// MakeRaw puts the terminal in key-at-a-time mode and returns what puts it
	// back. Nil, or an error, means no screen can run: Run returns ErrNoRaw.
	MakeRaw func() (restore func(), err error)
	// Size is the frame's width and height; nil, or 0, means unknown.
	Size  func() (w, h int)
	Style Style
	// Tick, when set, sends Update a Tick that often while no key arrives.
	// It needs an In with SetReadDeadline; on a terminal file that has none,
	// Run reads /dev/tty instead, and with neither no Tick comes.
	Tick time.Duration
	// Signals delivers SIGINT and SIGTERM while the terminal is raw, Resize
	// delivers SIGWINCH; nil listens on the process. Exit ends the process
	// after the restore; nil is os.Exit.
	Signals <-chan os.Signal
	Resize  <-chan os.Signal
	Exit    func(code int)
}

// ErrNoRaw is Run's answer when the terminal cannot take raw mode. The
// caller falls back to plain output (the palette's numbered prompt).
var ErrNoRaw = errors.New("terminal cannot take raw mode")

// Escapes the driver writes around frames. Every frame starts with
// ClearScreen, so a test splits the output on it.
const (
	ClearScreen = "\x1b[H\x1b[2J"
	HideCursor  = "\x1b[?25l"
	ShowCursor  = "\x1b[?25h"
)

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

type deadliner interface{ SetReadDeadline(time.Time) error }

type driver struct {
	t     Terminal
	mu    sync.Mutex // guards cur and raw; held across a frame write
	cur   Model
	raw   bool
	leave func()
}

// Run shows m until a Cmd quits it or the input ends. The terminal is
// restored on every way out: a normal return, a panic (deferred), SIGINT and
// SIGTERM (restored before exiting 128+signal), and around every Cooked
// Exec. A SIGWINCH redraws the current frame at the new size.
func Run(t Terminal, m Model) error {
	if t.MakeRaw == nil {
		return ErrNoRaw
	}
	t.Out = &lockedWriter{w: t.Out}
	d := &driver{t: t, cur: m}
	if err := d.enter(); err != nil {
		return fmt.Errorf("%w: %v", ErrNoRaw, err)
	}
	defer func() {
		if d.leave != nil {
			d.leave()
		}
	}()
	in, dl, closeIn := d.input()
	defer closeIn()

	buf := make([]byte, 64)
	next := time.Now().Add(t.Tick)
	for {
		d.draw()
		if dl != nil {
			dl.SetReadDeadline(next)
		}
		n, rerr := in.Read(buf)
		var msgs []Msg
		switch {
		case dl != nil && errors.Is(rerr, os.ErrDeadlineExceeded):
			msgs, next = []Msg{Tick{}}, time.Now().Add(t.Tick)
		case n == 0 && rerr != nil:
			return nil
		default:
			for _, k := range DecodeKeys(buf[:n]) {
				msgs = append(msgs, k)
			}
		}
		var cmd Cmd
		for _, msg := range msgs {
			m, cmd = m.Update(msg)
			if !cmd.zero() {
				break // the rest of the read was typed at the old screen
			}
		}
		for !cmd.zero() {
			d.set(m)
			if cmd.Exec == nil {
				return nil
			}
			var err error
			if cmd.Cooked {
				if dl != nil {
					dl.SetReadDeadline(time.Time{})
				}
				var stop bool
				if err, stop = d.handOver(cmd, in, buf); stop {
					return err
				}
			} else if err = cmd.Exec(); cmd.Quit {
				return err
			}
			m, cmd = m.Update(Done{Err: err})
		}
		d.set(m)
	}
}

// handOver runs a Cooked Exec on a cooked terminal and takes raw mode back.
// stop says Run ends, with err: the Cmd quits, raw mode is lost, or the
// input ended at "press any key".
func (d *driver) handOver(cmd Cmd, in io.Reader, buf []byte) (err error, stop bool) {
	d.leave()
	d.leave = nil
	fmt.Fprint(d.t.Out, ClearScreen)
	err = cmd.Exec()
	if cmd.Quit {
		return err, true
	}
	if cmd.Wait && err != nil {
		fmt.Fprintf(d.t.Out, "error: %v\n", err)
	}
	if rerr := d.enter(); rerr != nil {
		return rerr, true
	}
	if cmd.Wait {
		fmt.Fprint(d.t.Out, "\npress any key")
		if n, _ := in.Read(buf); n == 0 {
			return nil, true
		}
	}
	return err, false
}

// input is where keys come from. With a Tick it must take a read deadline:
// a terminal file opened by the shell often cannot, /dev/tty opened here can.
func (d *driver) input() (io.Reader, deadliner, func()) {
	in := d.t.In
	if d.t.Tick <= 0 {
		return in, nil, func() {}
	}
	if dl, ok := in.(deadliner); ok && dl.SetReadDeadline(time.Time{}) == nil {
		return in, dl, func() {}
	}
	if _, ok := in.(*os.File); ok {
		if tty, err := os.Open("/dev/tty"); err == nil {
			if tty.SetReadDeadline(time.Time{}) == nil {
				return tty, tty, func() { tty.Close() }
			}
			tty.Close()
		}
	}
	return in, nil, func() {}
}

func (d *driver) set(m Model) {
	d.mu.Lock()
	d.cur = m
	d.mu.Unlock()
}

func (d *driver) draw() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.raw {
		return
	}
	var w, h int
	if d.t.Size != nil {
		w, h = d.t.Size()
	}
	fmt.Fprint(d.t.Out, ClearScreen, d.cur.Render(w, h, d.t.Style))
}

// enter sets raw mode, hides the cursor and arms the signal handlers. d.leave
// undoes all of it, once.
func (d *driver) enter() error {
	restore, err := d.t.MakeRaw()
	if err != nil {
		return err
	}
	fmt.Fprint(d.t.Out, HideCursor)
	sigs, stopSigs := d.t.Signals, func() {}
	if sigs == nil {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
		sigs, stopSigs = ch, func() { signal.Stop(ch) }
	}
	resize, stopResize := d.t.Resize, func() {}
	if resize == nil {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGWINCH)
		resize, stopResize = ch, func() { signal.Stop(ch) }
	}
	exit := d.t.Exit
	if exit == nil {
		exit = os.Exit
	}
	d.mu.Lock()
	d.raw = true
	d.mu.Unlock()
	var once sync.Once
	done := make(chan struct{})
	leave := func() {
		once.Do(func() {
			d.mu.Lock()
			d.raw = false
			d.mu.Unlock()
			close(done)
			stopSigs()
			stopResize()
			fmt.Fprint(d.t.Out, ShowCursor)
			restore()
		})
	}
	d.leave = leave
	go func() {
		for {
			select {
			case sig := <-sigs:
				leave()
				code := 130
				if s, ok := sig.(syscall.Signal); ok {
					code = 128 + int(s)
				}
				exit(code)
				return
			case <-resize:
				d.draw()
			case <-done:
				return
			}
		}
	}()
	return nil
}
