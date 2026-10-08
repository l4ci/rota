// Package pidlive is the one rule for "is this pid still running": signal 0
// succeeds, and the process is not a zombie. Host (pane and worker liveness)
// and roundlease (the orchestrator lease) both use it, so a holder that has
// exited but is not yet reaped reads as gone everywhere.
package pidlive

import (
	"context"
	"errors"
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
