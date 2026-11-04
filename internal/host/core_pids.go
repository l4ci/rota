package host

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

// coreProc is one process incarnation a Core worker terminal spawned: the pid
// plus its start time when observed. A pid alone is not an identity, because
// the OS reuses numbers; a recorded pid whose current start time differs now
// belongs to an unrelated process and neither blocks cleanup nor counts as ours.
// Start is zero when it could not be read at observation time; that entry stays
// fail-closed (a live pid counts as ours), as does a live pid whose current
// start cannot be read.
type coreProc struct {
	PID   int    `json:"pid"`
	Start uint64 `json:"start,omitempty"`
}

// ours reports whether p is still the recorded process: alive, and started at
// the recorded time. Unknown on either side is no mismatch.
func (h *coreHost) ours(p coreProc) bool {
	if !h.d.Alive(p.PID) {
		return false
	}
	if p.Start == 0 {
		return true
	}
	cur, ok := h.d.Start(p.PID)
	return !ok || cur == p.Start
}

// identify pairs each pid with its start time as observed now, dropping
// duplicates and non-positive pids.
func (h *coreHost) identify(pids []int) []coreProc {
	seen := map[int]bool{}
	var out []coreProc
	for _, pid := range pids {
		if pid <= 0 || seen[pid] {
			continue
		}
		seen[pid] = true
		start, _ := h.d.Start(pid)
		out = append(out, coreProc{PID: pid, Start: start})
	}
	return out
}

// decodeCorePIDs reads a .pids file: the current list of {pid,start} objects,
// the legacy bare []int, or a mix. Legacy pids carry no identity.
//
// Legacy rule: a bare pid (from the old file or the handle's spawn-time list)
// is trusted only when it shows up again in the live terminal's process tree;
// the caller then records it with its start time. Anything else, including every
// bare pid once the terminal is gone, is ignored: without a start time nothing
// distinguishes the worker's child from an unrelated process that reused the
// number, and missing an orphan from before this format is better than blocking
// a slot forever on, or signalling, a stranger.
func decodeCorePIDs(b []byte) (procs []coreProc, legacy []int, err error) {
	var raw []json.RawMessage
	if err = json.Unmarshal(b, &raw); err != nil {
		return nil, nil, err
	}
	for _, r := range raw {
		var n int
		if json.Unmarshal(r, &n) == nil {
			legacy = append(legacy, n)
			continue
		}
		var p coreProc
		if err = json.Unmarshal(r, &p); err != nil {
			return nil, nil, err
		}
		procs = append(procs, p)
	}
	return procs, legacy, nil
}

func (h *coreHost) readCorePIDs(path string) (procs []coreProc, legacy []int, err error) {
	b, err := h.d.FS.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if procs, legacy, err = decodeCorePIDs(b); err != nil {
		return nil, nil, fmt.Errorf("Core process evidence corrupt")
	}
	return procs, legacy, nil
}

// coreEvidence is what a kill must prove gone.
type coreEvidence struct {
	procs    []coreProc
	observed []int // the live terminal's tree right now
}

func (ev coreEvidence) pids() []int {
	out := make([]int, 0, len(ev.procs))
	for _, p := range ev.procs {
		out = append(out, p.PID)
	}
	return out
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
	ev.procs = append(stored, h.identify(ev.observed)...)
	if len(ev.procs) == 0 && len(legacy)+len(v.PIDs) == 0 {
		return ev, fmt.Errorf("refusing Core kill without process evidence")
	}
	return ev, nil
}

// aliveDeps is h's deps with Alive answering "is this still our process" for the
// evidence, so killLoop ignores a pid another program now holds.
func (ev coreEvidence) aliveDeps(h *coreHost) Deps {
	d := h.d
	d.Alive = func(pid int) bool {
		for _, p := range ev.procs {
			if p.PID == pid && h.ours(p) {
				return true
			}
		}
		return false
	}
	return d
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
		now := h.identify(observed)
		fresh := map[int]coreProc{}
		for _, n := range now {
			fresh[n.PID] = n
		}
		procs := []coreProc{}
		for _, s := range stored {
			// A pid seen again with a readable start supersedes its stored
			// entry (this also upgrades an unknown start).
			if n, ok := fresh[s.PID]; (ok && n.Start > 0 && n.Start != s.Start) || !h.ours(s) {
				continue
			}
			procs = append(procs, s)
		}
		for _, n := range now {
			dup := false
			for _, s := range procs {
				dup = dup || s == n
			}
			if !dup {
				procs = append(procs, n)
			}
		}
		b, _ := json.Marshal(procs)
		return h.d.FS.WriteFileAtomic(p, b)
	})
}
