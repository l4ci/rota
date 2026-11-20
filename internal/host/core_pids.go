package host

import (
	"fmt"
)

func (h *coreHost) pidStore(v coreHandle) (evidenceStore, error) {
	p, e := h.recordPath(v)
	if e != nil {
		return evidenceStore{}, e
	}
	return evidenceStore{fs: h.d.FS, path: p + ".pids"}, nil
}

// readCorePIDs loads recorded incarnations. Legacy bare pids (no start time)
// are returned apart: Core trusts them only through the live terminal's tree.
func readCorePIDs(s evidenceStore) (procs processEvidence, legacy []int, err error) {
	f, err := s.Load()
	if err != nil {
		return nil, nil, fmt.Errorf("Core process evidence corrupt")
	}
	return f.Procs, f.Legacy, nil
}

// coreEvidence is what a kill must prove gone.
type coreEvidence struct {
	procs    processEvidence
	observed []int // the live terminal's tree right now
}

// killEvidence gathers the recorded incarnations plus the live terminal's tree.
// Legacy bare pids (the handle's and the old file's) are never counted or proved; they matter only through the tree.
func (h *coreHost) killEvidence(v coreHandle, t coreTerminal) (coreEvidence, error) {
	s, e := h.pidStore(v)
	if e != nil {
		return coreEvidence{}, e
	}
	stored, _, e := readCorePIDs(s)
	if e != nil {
		return coreEvidence{}, e
	}
	file, e := s.Load()
	if e != nil {
		return coreEvidence{}, fmt.Errorf("Core process evidence corrupt")
	}
	var ev coreEvidence
	if t.State == "live" && t.PID > 0 {
		ev.observed = h.d.Tree(t.PID)
	}
	ev.procs = stored.retain(h.d, ev.observed)
	// Fail closed on missing evidence: a kill needs either the live tree it
	// can observe now or the retained .pids file the spawn wrote. The handle's
	// bare v.PIDs carry no start times, so they prove nothing and are not
	// counted: with the file missing (the spawn-time write failed) and no live
	// tree, the proof set is empty and the kill would only prove the terminal
	// gone. A file that exists, even empty or legacy-only, records that the
	// evidence persisted and the proof set is exactly what it retains.
	if !file.Found && len(ev.observed) == 0 {
		return ev, fmt.Errorf("refusing Core kill without process evidence")
	}
	return ev, nil
}

// rememberPIDs persists the live terminal's tree with start times beside the
// immutable identity, so later descendants stay provable after their root
// exits. Entries that are dead or whose pid now has a different start time are
// dropped, which keeps a long-lived slot's list bounded to live processes.
func (h *coreHost) rememberPIDs(v coreHandle, observed []int) error {
	s, e := h.pidStore(v)
	if e != nil {
		return e
	}
	return s.Locked(func() error {
		stored, _, e := readCorePIDs(s)
		if e != nil {
			return e
		}
		return s.Save(stored.retain(h.d, observed))
	})
}
