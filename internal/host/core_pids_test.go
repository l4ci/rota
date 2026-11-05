package host

import (
	"context"
	"strings"
	"testing"
)

func corePidFixture(t *testing.T, tree ...int) (Host, *coreFake, *coreHost, *pidTable) {
	t.Helper()
	h, f := coreFixture(t)
	c := h.(*coreHost)
	tab := &pidTable{alive: map[int]bool{}, start: map[int]uint64{}, hidden: map[int]bool{}}
	c.d.Alive, c.d.Start, c.d.KillWait = tab.isAlive, tab.startOf, 1
	c.d.Tree = func(p int) []int { return append([]int{p}, tree...) }
	return h, f, c, tab
}

func readProcs(t *testing.T, c *coreHost, s string) processEvidence {
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
