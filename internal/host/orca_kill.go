package host

// Kill with process evidence.
import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

func (o *orca) Kill(ctx context.Context, slot, handle string) error {
	if handle == "" {
		return nil
	}
	h, e := o.decodeOrca(handle)
	if e != nil {
		return e
	}
	if h.ID == "" {
		return fmt.Errorf("unverifiable Orca close: native handle missing")
	}
	evidence, e := o.processes(h, orcaRefreshEvidence)
	if e != nil {
		return e
	}
	ts, e := o.terminals(ctx)
	if e != nil {
		return e
	}
	matches := []orcaTerminal{}
	for _, t := range ts {
		if t.ID == h.ID {
			matches = append(matches, t)
		}
	}
	if len(matches) > 1 {
		return fmt.Errorf("unverifiable Orca close: duplicate native handle")
	}
	// A runtime restart can remove the native handle without a prior close.
	// Absence is not process proof: the retained tree must still be all gone.
	// Round reclaim owns the --force guard for an unverifiable stale handle.
	probe := evidence.aliveDeps(o.d)
	out, e := runKill(killSteps{
		deps: &probe, pids: evidence.pids(),
		close: func() error {
			if len(matches) == 1 {
				_, err := o.call(ctx, "terminal", "close", "--terminal", h.ID, "--json")
				return err
			}
			return nil
		},
		gone: func() presence {
			ts, e := o.terminals(ctx)
			if e != nil {
				return presentYes
			}
			for _, t := range ts {
				if t.ID == h.ID && !t.Exited {
					return presentYes
				}
			}
			return presentGone
		},
	})
	if e != nil {
		return e
	}
	if !out.ok {
		var unknown []int
		for _, pid := range out.alive {
			for _, p := range evidence {
				if p.PID == pid && p.Start == 0 {
					unknown = append(unknown, pid)
					break
				}
			}
		}
		detail := ""
		if len(unknown) > 0 {
			detail = fmt.Sprintf("; no recorded start time (legacy or unreadable at observation):%s; inspect these processes before retrying cleanup", pidList(unknown))
		}
		return fmt.Errorf("orca close unproven; surviving processes:%s%s", pidList(out.alive), detail)
	}
	o.d.FS.Remove(h.PIDFile + ".json")
	return nil
}

type orcaEvidenceMode int

const (
	orcaReadEvidence orcaEvidenceMode = iota
	orcaRefreshEvidence
	orcaBootEvidence // only a newly created terminal may establish its root
)

// processes retains observations from boot, polling and cleanup. Serialize
// updates so a polling process cannot discard descendants saved by cleanup.
func (o *orca) processes(h orcaHandle, mode orcaEvidenceMode) (processEvidence, error) {
	var evidence processEvidence
	err := evidenceStore{fs: o.d.FS, path: h.PIDFile + ".tree"}.Locked(func() error {
		var err error
		evidence, err = o.processesLocked(h, mode)
		return err
	})
	return evidence, err
}

func (o *orca) processesLocked(h orcaHandle, mode orcaEvidenceMode) (processEvidence, error) {
	raw, e := o.d.FS.ReadFile(h.PIDFile)
	if e != nil {
		return nil, fmt.Errorf("missing Orca PID proof: %w", e)
	}
	pid, e := strconv.Atoi(strings.TrimSpace(string(raw)))
	if e != nil || pid <= 0 {
		return nil, fmt.Errorf("missing Orca PID proof")
	}
	var evidence processEvidence
	store := evidenceStore{fs: o.d.FS, path: h.PIDFile + ".tree"}
	f, e := store.Load()
	if e != nil {
		if errors.Is(e, errEvidenceCorrupt) {
			return nil, fmt.Errorf("unreadable Orca tree proof")
		}
		return nil, e
	}
	if f.Found {
		if f.Bare && len(f.Procs)+len(f.Legacy) == 0 {
			return nil, fmt.Errorf("unreadable Orca tree proof")
		}
		// Old Orca numbers remain unknown, conservative blockers. Never
		// upgrade them from, or expand the tree of, today's process table.
		evidence = append(f.Procs, unknownProcesses(f.Legacy)...)
	} else if mode != orcaBootEvidence || !o.d.Alive(pid) {
		return nil, fmt.Errorf("missing Orca retained process-tree proof; inspect the slot processes before retrying cleanup")
	} else {
		// First observation of the newly launched worker, before its root
		// exits. Subsequent refreshes require the retained incarnation.
		evidence = observeProcesses(o.d, append([]int{pid}, o.d.Tree(pid)...))
	}
	if mode != orcaReadEvidence {
		evidence = evidence.refresh(o.d)
		if e := store.Save(evidence); e != nil {
			return nil, e
		}
	}
	return evidence, nil
}

// Only rota-owned, retained process evidence can prove a terminal agentless;
// connected/writable/orphaned and absent runtime handles cannot.
func (o *orca) processesGone(h orcaHandle) bool {
	evidence, err := o.processes(h, orcaReadEvidence)
	if err != nil {
		return false
	}
	probe := evidence.aliveDeps(o.d)
	for _, pid := range evidence.pids() {
		if probe.Alive(pid) {
			return false
		}
	}
	return true
}
