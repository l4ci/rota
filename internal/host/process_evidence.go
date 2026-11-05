package host

import "encoding/json"

// processIdentity records an incarnation, not ownership of a numeric PID.
// An unknown start (at observation or at cleanup) stays fail-closed while alive.
type processIdentity struct {
	PID   int    `json:"pid"`
	Start uint64 `json:"start,omitempty"`
}

type processEvidence []processIdentity

func (p processIdentity) survives(d Deps) bool {
	if !d.Alive(p.PID) {
		return false
	}
	cur, ok := d.Start(p.PID)
	return p.Start == 0 || !ok || cur == p.Start
}

func observeProcesses(d Deps, pids []int) processEvidence {
	seen := map[int]bool{}
	var out processEvidence
	for _, pid := range pids {
		if pid <= 0 || seen[pid] {
			continue
		}
		seen[pid] = true
		start, ok := d.Start(pid)
		if !ok {
			start = 0
		}
		out = append(out, processIdentity{PID: pid, Start: start})
	}
	return out
}

// retain accepts fresh observations only from an independently trusted tree.
// Dead/reused incarnations disappear; fresh readable starts upgrade unknowns.
func (ev processEvidence) retain(d Deps, observed []int) processEvidence {
	now := observeProcesses(d, observed)
	fresh := map[int]processIdentity{}
	for _, p := range now {
		fresh[p.PID] = p
	}
	out := processEvidence{}
	for _, p := range ev {
		if n, ok := fresh[p.PID]; (ok && n.Start > 0 && n.Start != p.Start) || !p.survives(d) {
			continue
		}
		out = append(out, p)
	}
	for _, n := range now {
		dup := false
		for _, p := range out {
			dup = dup || p == n
		}
		if !dup {
			out = append(out, n)
		}
	}
	return out
}

// refresh discovers children of retained processes only with a readable,
// matching incarnation. Unknown identity can block cleanup but cannot grant
// ownership of a current stranger's descendants or acquire its start time.
func (ev processEvidence) refresh(d Deps) processEvidence {
	var observed []int
	for _, p := range ev {
		if p.Start == 0 || !d.Alive(p.PID) {
			continue
		}
		if cur, ok := d.Start(p.PID); ok && cur == p.Start {
			observed = append(observed, d.Tree(p.PID)...)
		}
	}
	return ev.retain(d, observed)
}

func (ev processEvidence) pids() []int {
	out := make([]int, 0, len(ev))
	for _, p := range ev {
		out = append(out, p.PID)
	}
	return out
}

func (ev processEvidence) aliveDeps(d Deps) Deps {
	probe := d
	probe.Alive = func(pid int) bool {
		for _, p := range ev {
			if p.PID == pid && p.survives(d) {
				return true
			}
		}
		return false
	}
	return probe
}

// decodeProcessEvidence keeps legacy bare PIDs separate: they have no
// historical start time. Adapters choose their established compatibility rule
// explicitly, rather than identifying an old number from today's process table.
func decodeProcessEvidence(b []byte) (procs processEvidence, legacy []int, err error) {
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
		var p processIdentity
		if err = json.Unmarshal(r, &p); err != nil {
			return nil, nil, err
		}
		procs = append(procs, p)
	}
	return procs, legacy, nil
}

// unknownProcesses preserves legacy Orca's conservative blocker semantics,
// without assigning the bare numbers a current process's identity.
func unknownProcesses(pids []int) processEvidence {
	out := make(processEvidence, 0, len(pids))
	for _, pid := range pids {
		out = append(out, processIdentity{PID: pid})
	}
	return out
}
