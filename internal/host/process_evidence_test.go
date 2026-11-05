package host

import (
	"reflect"
	"sync"
	"testing"
)

// pidTable is a fake process table: which pids are alive and their start times.
type pidTable struct {
	mu     sync.Mutex
	alive  map[int]bool
	start  map[int]uint64
	hidden map[int]bool // start time unreadable
}

func (p *pidTable) isAlive(pid int) bool { p.mu.Lock(); defer p.mu.Unlock(); return p.alive[pid] }
func (p *pidTable) startOf(pid int) (uint64, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.hidden[pid] {
		return 0, false
	}
	s, ok := p.start[pid]
	return s, ok
}
func (p *pidTable) set(pid int, alive bool, start uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.alive[pid], p.start[pid] = alive, start
}

func evidenceFixture() (Deps, *pidTable) {
	p := &pidTable{alive: map[int]bool{}, start: map[int]uint64{}, hidden: map[int]bool{}}
	return Deps{Alive: p.isAlive, Start: p.startOf, Tree: func(pid int) []int { return []int{pid} }}, p
}

func TestProcessEvidenceRefreshAfterRootExit(t *testing.T) {
	d, p := evidenceFixture()
	p.set(123, true, 10)
	p.set(456, true, 100)
	ev := observeProcesses(d, []int{123, 456})
	p.set(123, false, 10)
	p.set(789, true, 300)
	d.Tree = func(pid int) []int { return []int{pid, 789} }
	ev = ev.refresh(d)
	if want := (processEvidence{{456, 100}, {789, 300}}); !reflect.DeepEqual(ev, want) {
		t.Fatalf("surviving child and its new descendant: got %v, want %v", ev, want)
	}
	// Reuse cannot seed another descendant, while a genuine retained one stays.
	p.set(456, true, 200)
	p.set(999, true, 400)
	d.Tree = func(pid int) []int {
		if pid == 456 {
			return []int{456, 999}
		}
		return []int{pid}
	}
	ev = ev.refresh(d)
	if want := (processEvidence{{789, 300}}); !reflect.DeepEqual(ev, want) {
		t.Fatalf("reused PID acquired descendants: got %v, want %v", ev, want)
	}
	probe := ev.aliveDeps(d)
	if !probe.Alive(789) || probe.Alive(456) || probe.Alive(999) {
		t.Fatal("cleanup probe lost incarnation ownership")
	}
}

func TestProcessEvidenceUnknownIdentityBlocksWithoutExpanding(t *testing.T) {
	for _, unknownAtObservation := range []bool{false, true} {
		t.Run(map[bool]string{false: "current-unreadable", true: "observed-unreadable"}[unknownAtObservation], func(t *testing.T) {
			d, p := evidenceFixture()
			p.set(456, true, 100)
			p.hidden[456] = unknownAtObservation
			d.Start = func(pid int) (uint64, bool) {
				start, ok := p.startOf(pid)
				if !ok {
					return 999, false // only a successful read carries an identity
				}
				return start, true
			}
			ev := observeProcesses(d, []int{456})
			if unknownAtObservation && ev[0].Start != 0 {
				t.Fatalf("invented an observation identity: %v", ev)
			}
			p.hidden[456] = !unknownAtObservation
			d.Tree = func(int) []int { return []int{456, 789} }
			ev = ev.refresh(d)
			probe := ev.aliveDeps(d)
			if len(ev) != 1 || !probe.Alive(456) || probe.Alive(789) {
				t.Fatalf("unknown identity must block without expanding: %v", ev)
			}
			if unknownAtObservation && ev[0].Start != 0 {
				t.Fatalf("assigned today's identity to an unknown old PID: %v", ev)
			}
			p.set(456, false, 100)
			if got := ev.refresh(d); len(got) != 0 {
				t.Fatalf("exited unknown process kept: %v", got)
			}
		})
	}
}

func TestProcessEvidenceRetentionPrunesAndUpgradesTrustedObservations(t *testing.T) {
	d, p := evidenceFixture()
	p.set(42, true, 10)
	p.hidden[42] = true
	ev := observeProcesses(d, []int{42})
	p.hidden[42] = false
	ev = ev.retain(d, []int{42, 42, -1})
	if want := (processEvidence{{42, 10}}); !reflect.DeepEqual(ev, want) {
		t.Fatalf("trusted tree did not upgrade unknown start or deduplicate: %v", ev)
	}
	for i := 0; i < 60; i++ {
		pid := 1000 + i
		p.set(pid, true, uint64(i+1))
		if i > 0 {
			p.set(pid-1, false, 0)
		}
		ev = ev.retain(d, []int{42, pid})
	}
	p.set(1059, true, 777)
	ev = ev.retain(d, []int{42})
	if want := (processEvidence{{42, 10}}); !reflect.DeepEqual(ev, want) {
		t.Fatalf("dead/reused observations accumulated: %v", ev)
	}
}

func TestProcessEvidenceLegacyCompatibility(t *testing.T) {
	procs, legacy, err := decodeProcessEvidence([]byte(`[3, {"pid":4,"start":9}, {"pid":5}]`))
	if err != nil || !reflect.DeepEqual(procs, processEvidence{{4, 9}, {PID: 5}}) || !reflect.DeepEqual(legacy, []int{3}) {
		t.Fatalf("procs=%v legacy=%v err=%v", procs, legacy, err)
	}
	for _, bad := range []string{`{"pid":1}`, `["x"]`} {
		if _, _, err := decodeProcessEvidence([]byte(bad)); err == nil {
			t.Fatalf("accepted invalid evidence: %s", bad)
		}
	}
	d, p := evidenceFixture()
	p.set(3, true, 999)
	d.Tree = func(int) []int { return []int{3, 789} }
	ev := unknownProcesses(legacy).refresh(d)
	if want := (processEvidence{{PID: 3}}); !reflect.DeepEqual(ev, want) {
		t.Fatalf("legacy evidence acquired current incarnation/descendants: %v", ev)
	}
	probe := ev.aliveDeps(d)
	if !probe.Alive(3) || probe.Alive(789) {
		t.Fatal("legacy unknown process must conservatively block alone")
	}
}
