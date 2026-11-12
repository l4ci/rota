package pidlive

import (
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/proc"
)

// Listener is one TCP listener: its port and the pid that holds it. PID is 0
// when the scanner may not see the owner.
type Listener struct{ Port, PID int }

// ScanFunc runs the listener scanner name with args and returns its stdout;
// ok is false when the tool is missing or exited non-zero. A test of the
// listener scan sets Real.Scan.
type ScanFunc func(name string, args ...string) (stdout string, ok bool)

func (r Real) scan(name string, args ...string) (string, bool) {
	if r.Scan != nil {
		return r.Scan(name, args...)
	}
	bin, err := exec.LookPath(name)
	if err != nil {
		return "", false
	}
	res, err := proc.Run(context.Background(), proc.Cmd{Name: bin, Args: args, Env: []string{"LC_ALL=C"}})
	return res.Stdout, err == nil && res.ExitCode == 0
}

// Listeners reads the machine's TCP listeners with ss, else lsof. ok is false
// when neither is installed or the scan failed.
func (r Real) Listeners() ([]Listener, bool) {
	if out, ok := r.scan("ss", "-ltnpH"); ok {
		return parseSS(out), true
	}
	if out, ok := r.scan("lsof", "-nP", "-iTCP", "-sTCP:LISTEN", "-Fpn"); ok {
		return parseLsofListeners(out), true
	}
	return nil, false
}

var (
	ssPort = regexp.MustCompile(`:(\d+)\s`)
	ssPID  = regexp.MustCompile(`pid=(\d+)`)
)

// addListener appends l unless the port and pid are already listed: one
// listener per address family is one listener.
func addListener(ls []Listener, l Listener) []Listener {
	for _, o := range ls {
		if o == l {
			return ls
		}
	}
	return append(ls, l)
}

// parseSS reads `ss -ltnpH` output. A listener whose process ss may not see
// has PID 0.
func parseSS(out string) []Listener {
	var ls []Listener
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		m := ssPort.FindStringSubmatch(f[3] + " ")
		if m == nil {
			continue
		}
		l := Listener{}
		l.Port, _ = strconv.Atoi(m[1])
		if p := ssPID.FindStringSubmatch(line); p != nil {
			l.PID, _ = strconv.Atoi(p[1])
		}
		ls = addListener(ls, l)
	}
	return ls
}

// parseLsofListeners reads `lsof -nP -iTCP -sTCP:LISTEN -Fpn` output. Its
// sibling ParseLsofCwd reads the `-d cwd` form of the same -F format.
func parseLsofListeners(out string) []Listener {
	var ls []Listener
	pid := 0
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "p"):
			pid, _ = strconv.Atoi(line[1:])
		case strings.HasPrefix(line, "n"):
			if i := strings.LastIndex(line, ":"); i >= 0 {
				if port, err := strconv.Atoi(line[i+1:]); err == nil {
					ls = addListener(ls, Listener{Port: port, PID: pid})
				}
			}
		}
	}
	return ls
}
