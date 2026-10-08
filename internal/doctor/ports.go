package doctor

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// SlotBlock is one live worker slot and the port block rota reserved for it.
type SlotBlock struct {
	Name, Worktree string
	Base, Size     int // ports Base .. Base+Size-1
}

// Listener is one TCP listener: its port, its process and that process's
// working directory ("" when it could not be read).
type Listener struct {
	Port, PID int
	Cwd       string
}

var (
	ssPort = regexp.MustCompile(`:(\d+)\s`)
	ssPID  = regexp.MustCompile(`pid=(\d+)`)
)

// ParseSS reads `ss -ltnpH` output. A listener whose process ss may not see
// has PID 0.
func ParseSS(out string) []Listener {
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
		dup := false
		for _, o := range ls {
			dup = dup || o.Port == l.Port && o.PID == l.PID
		}
		if !dup { // one listener per address family
			ls = append(ls, l)
		}
	}
	return ls
}

// ParseLsofCwd reads `lsof -a -p PID -d cwd -Fn` output: the directory, or "".
func ParseLsofCwd(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "n") {
			return line[1:]
		}
	}
	return ""
}

// ParseLsof reads `lsof -nP -iTCP -sTCP:LISTEN -Fpn` output.
func ParseLsof(out string) []Listener {
	var ls []Listener
	pid := 0
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "p"):
			pid, _ = strconv.Atoi(line[1:])
		case strings.HasPrefix(line, "n"):
			if i := strings.LastIndex(line, ":"); i >= 0 {
				if port, err := strconv.Atoi(line[i+1:]); err == nil {
					dup := false
					for _, l := range ls {
						dup = dup || l.Port == port && l.PID == pid
					}
					if !dup { // one listener per address family
						ls = append(ls, Listener{Port: port, PID: pid})
					}
				}
			}
		}
	}
	return ls
}

func within(dir, p string) bool {
	if dir == "" || p == "" {
		return false
	}
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// listeners reads the machine's TCP listeners with ss, else lsof; ok is false
// when neither is installed or the scan failed.
func (d *runner) listeners() (ls []Listener, ok bool) {
	if bin, found := d.in.Look("ss"); found {
		if r, ok := d.run(bin, []string{"-ltnpH"}, nil); ok && r.ExitCode == 0 {
			return ParseSS(r.Stdout), true
		}
	}
	if bin, found := d.in.Look("lsof"); found {
		if r, ok := d.run(bin, []string{"-nP", "-iTCP", "-sTCP:LISTEN", "-Fpn"}, nil); ok && r.ExitCode == 0 {
			return ParseLsof(r.Stdout), true
		}
	}
	return nil, false
}

// ports warns when a process outside a live slot's worktree listens inside
// that slot's port block. It reports nothing when no slot holds a block or
// every listener belongs to its slot, and a skip when the listeners could not
// be read (neither ss nor lsof ran). A listener whose owner cannot be read counts as foreign: nothing proves it
// belongs to the slot.
func (d *runner) ports() (Check, bool) {
	if len(d.in.Slots) == 0 {
		return Check{}, false
	}
	ls, ok := d.listeners()
	if !ok {
		return skip("ports", "cannot scan listeners: ss and lsof are missing or failed, so port blocks are not checked"), true
	}
	var found []string
	for _, b := range d.in.Slots {
		for _, l := range ls {
			if l.Port < b.Base || l.Port >= b.Base+b.Size {
				continue
			}
			cwd := l.Cwd
			if cwd == "" && l.PID > 0 && d.in.CwdOf != nil {
				cwd = d.in.CwdOf(l.PID)
			}
			if within(b.Worktree, cwd) {
				continue
			}
			who := "an unknown process"
			if l.PID > 0 {
				who = fmt.Sprintf("pid %d", l.PID)
				if cwd != "" {
					who += " in " + cwd
				}
			}
			found = append(found, fmt.Sprintf("port %d in slot %s's block %d-%d is held by %s", l.Port, b.Name, b.Base, b.Base+b.Size-1, who))
		}
	}
	if len(found) == 0 {
		return Check{}, false
	}
	return Check{Name: "ports", Status: Warn, Detail: strings.Join(found, "; "),
		Hint: "stop that process, or move the range: rota config set work.portBase <port>"}, true
}
