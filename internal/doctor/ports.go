package doctor

import (
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/pidlive"
	"github.com/l4ci/rota/internal/worker"
)

// SlotBlock is one live worker slot and the port block rota reserved for it.
type SlotBlock struct {
	Name, Worktree string
	Base, Size     int // ports Base .. Base+Size-1
}

// ports warns when a process outside a live slot's worktree listens inside
// that slot's port block. It reports nothing when no slot holds a block or
// every listener belongs to its slot, and a skip when the listeners could not
// be read (neither ss nor lsof ran, or no process source was given). A listener whose
// owner cannot be read counts as foreign: nothing proves it belongs to the slot.
func (d *runner) ports() (Check, bool) {
	if len(d.in.Slots) == 0 {
		return Check{}, false
	}
	var ls []pidlive.Listener
	ok := false
	if d.in.Procs != nil {
		ls, ok = d.in.Procs.Listeners(d.ctx)
	}
	if !ok {
		return skip("ports", "cannot scan listeners: ss and lsof are missing or failed, so port blocks are not checked"), true
	}
	var found []string
	for _, b := range d.in.Slots {
		for _, l := range ls {
			if l.Port < b.Base || l.Port >= b.Base+b.Size {
				continue
			}
			cwd := ""
			if l.PID > 0 {
				cwd = d.in.Procs.Cwd(l.PID)
			}
			if worker.PathContains(b.Worktree, cwd) {
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
		Hint: "stop that process, or move the range: rota config set round.portBase <port>"}, true
}
