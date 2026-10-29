// Package pidlive is the one rule for "is this pid still running": signal 0
// succeeds, and the process is not a zombie. Host (pane and worker liveness)
// and roundlease (the orchestrator lease) both use it, so a holder that has
// exited but is not yet reaped reads as gone everywhere.
package pidlive

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/l4ci/rota/internal/proc"
)

// State reads a pid's ps state letters ("S", "Z+", ...). ok is false when it
// could not be read.
type State func(pid int) (stat string, ok bool)

// Alive reports whether pid is a running, non-zombie process on this host.
func Alive(pid int) bool { return AliveWith(psState, pid) }

// AliveWith is Alive with the process-state read injected.
//
// kill -0 succeeds for a zombie, so the state is read too. EPERM means the
// process exists under another user: alive. An unreadable state falls back to
// the signal result. A zombie is dead on purpose: it has exited and can never
// act, so a lease it holds would otherwise be stuck until its parent reaps it.
func AliveWith(state State, pid int) bool {
	if pid <= 0 {
		return false
	}
	if err := syscall.Kill(pid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	stat, ok := state(pid)
	if !ok {
		return true
	}
	stat = strings.TrimSpace(stat)
	return stat != "" && !strings.HasPrefix(stat, "Z")
}

func psState(pid int) (string, bool) {
	r, err := proc.Run(context.Background(), proc.Cmd{Name: "ps", Args: []string{"-o", "stat=", "-p", strconv.Itoa(pid)}})
	if err != nil {
		return "", false
	}
	return r.Stdout, true
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

// StartTicks is the start time half of ProcStat.
func StartTicks(pid int) (uint64, bool) {
	_, st, ok := ProcStat(pid)
	return st, ok
}

// Comm reads the command name of pid from /proc/<pid>/comm. It is empty where
// /proc is absent or unreadable.
func Comm(pid int) string {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
