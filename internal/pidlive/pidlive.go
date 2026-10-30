// Package pidlive is the one owner of process introspection: whether a pid is
// still running (signal 0 succeeds and it is not a zombie), and its parent,
// start time, command name, working directory and descendants. Host, roundlease,
// doctor and worker read the process table only through a Source, so a test
// replaces it with a recorded table instead of the real ps and /proc.
package pidlive

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/l4ci/rota/internal/proc"
)

// Source reads facts about processes on one host. Real is the machine's;
// callers inject a fake table in tests.
type Source interface {
	// State is the ps state letters ("S", "Z+", ...); ok is false when it
	// could not be read.
	State(pid int) (stat string, ok bool)
	// Stat is the parent pid and start time of pid. The start time is only
	// comparable with another read from the same Source.
	Stat(pid int) (ppid int, start uint64, ok bool)
	// Comm is the command name, "" when unreadable.
	Comm(pid int) string
	// Cwd is the working directory, "" when unreadable (someone else's process).
	Cwd(pid int) string
	// Parents maps every pid to its parent pid; ok is false when the table
	// could not be listed.
	Parents() (ppids map[int]int, ok bool)
}

// PSFunc runs ps with args and returns its trimmed stdout.
type PSFunc func(args ...string) (string, error)

// Real is the Source over this machine: /proc where it exists, ps (and lsof for
// a working directory) where it does not (macOS). PS replaces the ps runner; a
// test of the ps parsing sets it.
type Real struct{ PS PSFunc }

func (r Real) ps(args ...string) (string, error) {
	if r.PS != nil {
		return r.PS(args...)
	}
	res, err := proc.Run(context.Background(), proc.Cmd{Name: "ps", Args: args, Env: []string{"LC_ALL=C"}})
	if err == nil && res.ExitCode != 0 {
		err = fmt.Errorf("ps exited %d", res.ExitCode)
	}
	return strings.TrimSpace(res.Stdout), err
}

// Alive reports whether pid is a running, non-zombie process on this host.
func Alive(pid int) bool { return AliveIn(Real{}, pid) }

// AliveIn is Alive reading the process state from src.
//
// kill -0 succeeds for a zombie, so the state is read too. EPERM means the
// process exists under another user: alive. An unreadable state falls back to
// the signal result. A zombie is dead on purpose: it has exited and can never
// act, so a lease it holds would otherwise be stuck until its parent reaps it.
func AliveIn(src Source, pid int) bool {
	if pid <= 0 {
		return false
	}
	if err := syscall.Kill(pid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	stat, ok := src.State(pid)
	if !ok {
		return true
	}
	stat = strings.TrimSpace(stat)
	return stat != "" && !strings.HasPrefix(stat, "Z")
}

// Running reports whether pid is still the process that was recorded: alive,
// and when a start time (non-zero) was recorded, started at that time. A start
// time src cannot read is no mismatch.
func Running(src Source, pid int, start uint64) bool {
	if !AliveIn(src, pid) {
		return false
	}
	if start == 0 {
		return true
	}
	_, cur, ok := src.Stat(pid)
	return !ok || cur == start
}

// StartTicks is the machine's start time of pid.
func StartTicks(pid int) (uint64, bool) {
	_, st, ok := Real{}.Stat(pid)
	return st, ok
}

// Parent is src's parent pid and command name of pid; ok is false when either
// is unreadable, so a walk up the ancestry can tell "no answer" from a name.
func Parent(src Source, pid int) (ppid int, comm string, ok bool) {
	pp, _, ok := src.Stat(pid)
	comm = src.Comm(pid)
	return pp, comm, ok && comm != ""
}

// Tree lists root and its descendants, breadth first. An unreadable table
// leaves root alone.
func Tree(src Source, root int) []int {
	out := []int{root}
	ppids, ok := src.Parents()
	if !ok {
		return out
	}
	kids := map[int][]int{}
	for p, pp := range ppids {
		kids[pp] = append(kids[pp], p)
	}
	for i := 0; i < len(out); i++ {
		next := kids[out[i]]
		sort.Ints(next)
		out = append(out, next...)
	}
	return out
}

// State reads the ps state letters of pid.
func (r Real) State(pid int) (string, bool) {
	out, err := r.ps("-o", "stat=", "-p", strconv.Itoa(pid))
	if err != nil {
		return "", false
	}
	return out, true
}

// Stat reads ppid and start time from /proc/<pid>/stat (clock ticks since
// boot), or through ps where there is no /proc (unix seconds of lstart).
func (r Real) Stat(pid int) (ppid int, start uint64, ok bool) {
	if pp, st, ok := ProcStat(pid); ok {
		return pp, st, true
	}
	return r.psStat(pid)
}

// Comm reads the command name from /proc/<pid>/comm, or through ps (which
// reports a full path on macOS).
func (r Real) Comm(pid int) string {
	if c := procComm(pid); c != "" {
		return c
	}
	out, err := r.ps("-o", "comm=", "-p", strconv.Itoa(pid))
	if err != nil || out == "" {
		return ""
	}
	return filepath.Base(out)
}

// Cwd reads the working directory: /proc on Linux, lsof where /proc is absent.
func (r Real) Cwd(pid int) string {
	if p, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid)); err == nil {
		return p
	}
	res, err := proc.Run(context.Background(), proc.Cmd{Name: "lsof", Args: []string{"-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-Fn"}})
	if err != nil {
		return ""
	}
	return ParseLsofCwd(res.Stdout)
}

// Parents lists every pid with its parent through ps.
func (r Real) Parents() (map[int]int, bool) {
	out, err := r.ps("-A", "-o", "pid=", "-o", "ppid=")
	if err != nil {
		return nil, false
	}
	ppids := map[int]int{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		p, e1 := strconv.Atoi(f[0])
		pp, e2 := strconv.Atoi(f[1])
		if e1 == nil && e2 == nil {
			ppids[p] = pp
		}
	}
	return ppids, true
}

// psStat reads ppid and start time (unix seconds) of pid through ps, for hosts
// without /proc (macOS).
func (r Real) psStat(pid int) (ppid int, start uint64, ok bool) {
	out, err := r.ps("-o", "ppid=,lstart=", "-p", strconv.Itoa(pid))
	if err != nil {
		return 0, 0, false
	}
	f := strings.Fields(out)
	if len(f) != 6 { // ppid, then "Mon Oct 20 12:00:00 2026"
		return 0, 0, false
	}
	pp, err := strconv.Atoi(f[0])
	if err != nil {
		return 0, 0, false
	}
	t, err := time.ParseInLocation("Mon Jan _2 15:04:05 2006", strings.Join(f[1:], " "), time.Local)
	if err != nil || t.Unix() <= 0 {
		return 0, 0, false
	}
	return pp, uint64(t.Unix()), true
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

// ProcStat reads the parent pid and start time (clock ticks since boot) of pid
// from /proc/<pid>/stat. ok is false where /proc is absent or unreadable. It is
// the one parser of that file: the comm field may hold spaces and parentheses,
// so the fields after it are found from the last ")".
func ProcStat(pid int) (ppid int, start uint64, ok bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, 0, false
	}
	return parseProcStat(string(b))
}

// parseProcStat is ProcStat's parser over one /proc/<pid>/stat line.
func parseProcStat(s string) (ppid int, start uint64, ok bool) {
	i := strings.LastIndex(s, ")")
	if i < 0 {
		return 0, 0, false
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 20 {
		return 0, 0, false
	}
	pp, err1 := strconv.Atoi(f[1])
	st, err2 := strconv.ParseUint(f[19], 10, 64)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return pp, st, true
}

// procComm reads the command name of pid from /proc/<pid>/comm. It is empty
// where /proc is absent or unreadable.
func procComm(pid int) string {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
