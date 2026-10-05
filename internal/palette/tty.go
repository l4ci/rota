package palette

import (
	"errors"
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

func sttyWidth(f *os.File) int {
	if out, err := stty(f, "size"); err == nil {
		if _, c, ok := strings.Cut(out, " "); ok {
			if n, err := strconv.Atoi(c); err == nil && n > 0 {
				return n
			}
		}
	}
	if n, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && n > 0 {
		return n
	}
	return 80
}

// RunTerminal fills the terminal parts of cfg from the process: raw mode and
// width through stty on cfg.In, colors from NO_COLOR and TERM. A dumb or
// unknown TERM, or an input that is not a terminal file, takes the numbered
// prompt. cfg.In and cfg.Out are the caller's.
func RunTerminal(cfg Config) error {
	in, isFile := cfg.In.(*os.File)
	term := os.Getenv("TERM")
	dumb := term == "" || term == "dumb"
	cfg.Color = !dumb && os.Getenv("NO_COLOR") == ""
	if dumb || !isFile {
		cfg.Dumb = true
		return Run(cfg)
	}
	cfg.MakeRaw = func() (func(), error) { return sttyRaw(in) }
	cfg.Width = func() int { return sttyWidth(in) }
	return Run(cfg)
}
