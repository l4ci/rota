package host

import (
	"context"
	"encoding/json"
	"strings"
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

func corePidFixture(t *testing.T, tree ...int) (Host, *coreFake, *coreHost, *pidTable) {
	t.Helper()
	h, f := coreFixture(t)
	c := h.(*coreHost)
	tab := &pidTable{alive: map[int]bool{}, start: map[int]uint64{}, hidden: map[int]bool{}}
	c.d.Alive, c.d.Start, c.d.KillWait = tab.isAlive, tab.startOf, 1
	c.d.Tree = func(p int) []int { return append([]int{p}, tree...) }
	return h, f, c, tab
}

func readProcs(t *testing.T, c *coreHost, s string) []coreProc {
	t.Helper()
	v, e := c.owned(s)
	if e != nil {
		t.Fatal(e)
	}
	p, _ := c.recordPath(v)
	procs, _, e := c.readCorePIDs(p + ".pids")
	if e != nil {
		t.Fatal(e)
	}
	return procs
}

func writePIDs(t *testing.T, c *coreHost, s, raw string) {
	t.Helper()
	v, e := c.owned(s)
	if e != nil {
		t.Fatal(e)
	}
	p, _ := c.recordPath(v)
	if e = c.d.FS.WriteFileAtomic(p+".pids", []byte(raw)); e != nil {
		t.Fatal(e)
	}
}

func TestCoreKillIgnoresReusedPidButBlocksOnSurvivor(t *testing.T) {
	h, f, c, tab := corePidFixture(t, 77)
	tab.set(77, true, 100)
	s := coreSpawn(t, h)
	f.missing = true
	err := h.Kill(context.Background(), "dana", s)
	if err == nil || !strings.Contains(err.Error(), "cleanup unproved; surviving pids: 77") {
		t.Fatalf("genuine survivor (same start) must block: %v", err)
	}
	// The number is now held by an unrelated process.
	tab.set(77, true, 200)
	if err = h.Kill(context.Background(), "dana", s); err != nil {
		t.Fatalf("reused pid blocked cleanup: %v", err)
	}
	if f.kills != 0 {
		t.Fatalf("missing terminal was killed: %d", f.kills)
	}
	for _, p := range readProcs(t, c, s) {
		if p.PID == 77 {
			t.Fatalf("reused pid kept as evidence: %+v", p)
		}
	}
}

// Kill closes the terminal over RPC and only polls recorded pids; it never
// signals one. A live terminal is still closed while a stale recorded pid, now
// held by a stranger, is ignored.
func TestCoreKillClosesLiveTerminalPastReusedPid(t *testing.T) {
	h, f, _, tab := corePidFixture(t, 77)
	tab.set(77, true, 100)
	s := coreSpawn(t, h)
	tab.set(77, true, 200)
	c := h.(*coreHost)
	c.d.Tree = func(p int) []int { return []int{p} } // the stranger is not in the tree
	if err := h.Kill(context.Background(), "dana", s); err != nil {
		t.Fatal(err)
	}
	if f.kills != 1 {
		t.Fatalf("kills=%d", f.kills)
	}
}

func TestCoreKillStaysFailClosedWhenStartUnreadable(t *testing.T) {
	h, f, _, tab := corePidFixture(t, 77)
	tab.set(77, true, 100)
	s := coreSpawn(t, h)
	f.missing = true
	tab.hidden[77] = true // alive, but its start time can no longer be read
	err := h.Kill(context.Background(), "dana", s)
	if err == nil || !strings.Contains(err.Error(), "surviving pids: 77") {
		t.Fatalf("unreadable start must stay retained: %v", err)
	}
}

func TestCoreKillKeepsUnknownStartEvidence(t *testing.T) {
	h, f, c, tab := corePidFixture(t, 77)
	tab.alive[77] = true
	tab.hidden[77] = true // never readable: recorded without a start
	s := coreSpawn(t, h)
	f.missing = true
	var got coreProc
	for _, p := range readProcs(t, c, s) {
		if p.PID == 77 {
			got = p
		}
	}
	if got.PID != 77 || got.Start != 0 {
		t.Fatalf("want 77 recorded with unknown start, got %+v", got)
	}
	if err := h.Kill(context.Background(), "dana", s); err == nil {
		t.Fatal("live pid with unknown start must block")
	}
}

func TestCoreLegacyBarePidsIgnoredWithoutLiveTree(t *testing.T) {
	h, f, c, tab := corePidFixture(t)
	s := coreSpawn(t, h)
	// A legacy file from before start times: pid 77 is now a stranger.
	tab.set(77, true, 9)
	tab.set(88, true, 9)
	writePIDs(t, c, s, "[77,88]")
	f.missing = true
	if err := h.Kill(context.Background(), "dana", s); err != nil {
		t.Fatalf("legacy bare pids outside the live tree blocked cleanup: %v", err)
	}
}

func TestCoreLegacyBarePidRecordedWhenSeenInLiveTree(t *testing.T) {
	h, _, c, tab := corePidFixture(t, 77)
	tab.set(77, true, 100)
	s := coreSpawn(t, h)
	writePIDs(t, c, s, "[77,88]")
	tab.set(88, true, 9) // stranger, not in the tree
	err := h.Kill(context.Background(), "dana", s)
	if err == nil || !strings.Contains(err.Error(), "surviving pids: 77") || strings.Contains(err.Error(), "88") {
		t.Fatalf("legacy pid in the live tree must block, stranger must not: %v", err)
	}
	var found bool
	for _, p := range readProcs(t, c, s) {
		if p.PID == 88 {
			t.Fatalf("stranger kept: %+v", p)
		}
		if p.PID == 77 && p.Start == 100 {
			found = true
		}
	}
	if !found {
		t.Fatal("legacy pid not upgraded with its start time")
	}
}

func TestCoreDecodePIDsReadsBothFormats(t *testing.T) {
	procs, legacy, err := decodeCorePIDs([]byte(`[3, {"pid":4,"start":9}, {"pid":5}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(legacy) != 1 || legacy[0] != 3 || len(procs) != 2 || procs[0] != (coreProc{4, 9}) || procs[1] != (coreProc{PID: 5}) {
		t.Fatalf("procs=%v legacy=%v", procs, legacy)
	}
	if _, _, err = decodeCorePIDs([]byte(`{"pid":1}`)); err == nil {
		t.Fatal("non-list accepted")
	}
	if _, _, err = decodeCorePIDs([]byte(`["x"]`)); err == nil {
		t.Fatal("bad entry accepted")
	}
}

func TestCoreRememberPrunesDeadAndReusedPids(t *testing.T) {
	h, _, c, tab := corePidFixture(t)
	s := coreSpawn(t, h)
	v, _ := c.owned(s)
	for i := 0; i < 60; i++ {
		pid := 1000 + i
		tab.set(pid, true, uint64(i+1))
		if i > 0 {
			tab.set(pid-1, false, 0) // the previous child exited
		}
		if e := c.rememberPIDs(v, []int{42, pid}); e != nil {
			t.Fatal(e)
		}
	}
	// A recorded pid reused by a stranger is dropped on the next remember.
	tab.set(1059, true, 777)
	if e := c.rememberPIDs(v, []int{42}); e != nil {
		t.Fatal(e)
	}
	procs := readProcs(t, c, s)
	if len(procs) > 2 {
		t.Fatalf("list not bounded: %v", procs)
	}
	for _, p := range procs {
		if p.PID == 1059 {
			t.Fatalf("reused pid kept: %v", procs)
		}
	}
	p, _ := c.recordPath(v)
	b, _ := c.d.FS.ReadFile(p + ".pids")
	if !json.Valid(b) {
		t.Fatalf("evidence not json: %s", b)
	}
}
