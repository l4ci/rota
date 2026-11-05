package host

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

func (h *coreHost) readCorePIDs(path string) (procs processEvidence, legacy []int, err error) {
	b, err := h.d.FS.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if procs, legacy, err = decodeProcessEvidence(b); err != nil {
		return nil, nil, fmt.Errorf("Core process evidence corrupt")
	}
	return procs, legacy, nil
}

// coreEvidence is what a kill must prove gone.
type coreEvidence struct {
	procs    processEvidence
	observed []int // the live terminal's tree right now
}

// killEvidence gathers the recorded incarnations plus the live terminal's tree.
// Legacy bare pids (the handle's and the old file's) count only through the tree.
func (h *coreHost) killEvidence(v coreHandle, t coreTerminal) (coreEvidence, error) {
	p, _ := h.recordPath(v)
	stored, legacy, e := h.readCorePIDs(p + ".pids")
	if e != nil {
		return coreEvidence{}, e
	}
	var ev coreEvidence
	if t.State == "live" && t.PID > 0 {
		ev.observed = h.d.Tree(t.PID)
	}
	ev.procs = stored.retain(h.d, ev.observed)
	if len(stored)+len(ev.observed)+len(legacy)+len(v.PIDs) == 0 {
		return ev, fmt.Errorf("refusing Core kill without process evidence")
	}
	return ev, nil
}

// rememberPIDs persists the live terminal's tree with start times beside the
// immutable identity, so later descendants stay provable after their root
// exits. Entries that are dead or whose pid now has a different start time are
// dropped, which keeps a long-lived slot's list bounded to live processes.
func (h *coreHost) rememberPIDs(v coreHandle, observed []int) error {
	p, e := h.recordPath(v)
	if e != nil {
		return e
	}
	p += ".pids"
	return h.d.FS.Locked(p, 30*time.Second, func() error {
		stored, _, e := h.readCorePIDs(p)
		if e != nil {
			return e
		}
		procs := stored.retain(h.d, observed)
		b, _ := json.Marshal(procs)
		return h.d.FS.WriteFileAtomic(p, b)
	})
}
