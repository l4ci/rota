package tui

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// stty runs stty against f, the controlling terminal; stty has no portable
// "-F" flag, but every flavor acts on its own standard input.
func stty(f *os.File, args ...string) (string, error) {
	cmd := exec.Command("stty", args...)
	cmd.Stdin = f
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// sttyRaw puts f in key-at-a-time mode (no line buffering, no echo, and Ctrl-C
// arrives as a byte) and returns the function that puts it back. Output
// processing stays on so "\n" still returns the carriage.
func sttyRaw(f *os.File) (func(), error) {
	saved, err := stty(f, "-g")
	if err != nil || saved == "" {
		return nil, errors.New("stty -g failed")
	}
	restore := func() { stty(f, saved) }
	if _, err := stty(f, "-icanon", "-echo", "-isig", "min", "1", "time", "0"); err != nil {
		restore()
		return nil, err
	}
	return restore, nil
}

// sttySize is the terminal's columns and rows, from stty, else COLUMNS and
// LINES, else 80 columns and an unknown (0) height.
func sttySize(f *os.File) (w, h int) {
	if out, err := stty(f, "size"); err == nil {
		r, c, _ := strings.Cut(out, " ")
		w, _ = strconv.Atoi(c)
		h, _ = strconv.Atoi(r)
	}
	if w <= 0 {
		w, _ = strconv.Atoi(os.Getenv("COLUMNS"))
	}
	if h <= 0 {
		h, _ = strconv.Atoi(os.Getenv("LINES"))
	}
	if w <= 0 {
		w = 80
	}
	return w, max(h, 0)
}

// NewTerminal fills a Terminal from the process: raw mode and size through
// stty on in, the style from NO_COLOR and TERM. ok is false when no screen
// can run: a dumb or unset TERM, or an in that is not a terminal file. in and
// out are the caller's.
func NewTerminal(in io.Reader, out io.Writer) (t Terminal, ok bool) {
	t = Terminal{In: in, Out: out, Style: EnvStyle()}
	f, isFile := in.(*os.File)
	if Dumb() || !isFile {
		return t, false
	}
	t.MakeRaw = func() (func(), error) { return sttyRaw(f) }
	t.Size = func() (int, int) { return sttySize(f) }
	return t, true
}
