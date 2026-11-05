package host

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOrcaRecoveryReusedChildDoesNotOwnStrangers(t *testing.T) {
	f := newOrcaFake(t)
	f.alive[456], f.alive[789] = true, true
	starts := map[int]uint64{123: 10, 456: 100, 789: 300}
	deps := f.deps()
	deps.Start = func(pid int) (uint64, bool) { s, ok := starts[pid]; return s, ok }
	deps.Tree = func(pid int) []int {
		if pid == 123 {
			return []int{123, 456}
		}
		if pid == 456 && starts[456] == 200 {
			return []int{456, 789}
		}
		return []int{pid}
	}
	adapter := New(Orca, deps)
	h, err := adapter.Spawn(context.Background(), SpawnOpts{Session: "r", Slot: "ben", Cwd: f.cwd, Launch: "codex", BootTimeout: 4})
	if err != nil {
		t.Fatal(err)
	}
	f.alive[123], starts[456] = false, 200
	tabs, err := adapter.Capabilities().TabLister.Tabs(context.Background())
	if err != nil || len(tabs) != 1 || !tabs[0].Agentless {
		t.Fatalf("reused child still owns terminal: tabs=%+v err=%v", tabs, err)
	}
	agents, err := adapter.Capabilities().Snapshotter.Snapshot(context.Background())
	if err != nil || len(agents) != 0 {
		t.Fatalf("reused child still counts as agent: agents=%+v err=%v", agents, err)
	}
	terminals := []orcaTerminal{}
	f.list = &terminals
	if err := adapter.Kill(context.Background(), "ben", h); err != nil {
		t.Fatalf("unrelated reused child blocked cleanup: %v", err)
	}
	meta, _ := decodeOrca(h)
	raw, err := os.ReadFile(meta.PIDFile + ".tree")
	if err != nil || !json.Valid(raw) {
		t.Fatalf("retained evidence: %s, %v", raw, err)
	}
	if strings.Contains(string(raw), "789") {
		t.Fatalf("unrelated process seeded retained descendants: %s", raw)
	}
	if f.closed || !f.alive[456] || !f.alive[789] {
		t.Fatal("cleanup acted on absent terminal or unrelated processes")
	}
}

func TestOrcaRecoveryLegacyEvidenceDoesNotAdoptCurrentProcesses(t *testing.T) {
	f := newOrcaFake(t)
	h := f.handle(t)
	meta, _ := decodeOrca(h)
	if err := os.WriteFile(meta.PIDFile+".tree", []byte("[123,456]"), 0600); err != nil {
		t.Fatal(err)
	}
	f.alive[123], f.alive[456], f.alive[789] = false, true, true
	terminals := []orcaTerminal{}
	f.list = &terminals
	deps := f.deps()
	deps.Tree = func(pid int) []int { return []int{pid, 789} }
	adapter := New(Orca, deps)
	err := adapter.Kill(context.Background(), "ben", h)
	if err == nil || !strings.Contains(err.Error(), "456") || strings.Contains(err.Error(), "789") {
		t.Fatalf("legacy process must block without adopting descendants: %v", err)
	}
	raw, err := os.ReadFile(meta.PIDFile + ".tree")
	if err != nil {
		t.Fatal(err)
	}
	var record orcaProcessRecord
	if err := json.Unmarshal(raw, &record); err != nil || record.Processes == nil {
		t.Fatalf("migrated legacy proof: %s, %v", raw, err)
	}
	if got := *record.Processes; len(got) != 1 || got[0] != (processIdentity{PID: 456}) {
		t.Fatalf("legacy PID acquired a current start identity: %v", got)
	}
	f.alive[456] = false
	if err := adapter.Kill(context.Background(), "ben", h); err != nil {
		t.Fatalf("exited legacy process blocked cleanup: %v", err)
	}
	// A versioned empty observation is still valid proof after another read.
	f.alive[123] = true // a new occupant of the old numeric root must stay unrelated
	if err := adapter.Kill(context.Background(), "ben", h); err != nil {
		t.Fatalf("proved empty observation could not be read again: %v", err)
	}
}

func TestOrcaRecoveryRejectsInvalidRetainedEvidence(t *testing.T) {
	for _, raw := range []string{"[]", "null", "[0]", "[{}]", `{"version":1}`, `{"version":1,"processes":null}`, `{"version":2,"processes":[]}`} {
		t.Run(raw, func(t *testing.T) {
			f := newOrcaFake(t)
			h := f.handle(t)
			meta, _ := decodeOrca(h)
			if err := os.WriteFile(meta.PIDFile+".tree", []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			if err := New(Orca, f.deps()).Kill(context.Background(), "ben", h); err == nil || f.closed {
				t.Fatalf("invalid proof authorized cleanup: %v closed=%v", err, f.closed)
			}
		})
	}
}

func TestOrcaRecoveryMissingTreeCannotAdoptOldPID(t *testing.T) {
	f := newOrcaFake(t)
	h := f.handle(t) // old PID proof, without a retained incarnation observation
	meta, _ := decodeOrca(h)
	f.alive[456] = true
	terminals := []orcaTerminal{}
	f.list = &terminals
	err := New(Orca, f.deps()).Kill(context.Background(), "ben", h)
	if err == nil || !strings.Contains(err.Error(), "missing Orca retained process-tree proof") {
		t.Fatalf("missing history was replaced with today's processes: %v", err)
	}
	if _, err := os.Stat(meta.PIDFile + ".tree"); !os.IsNotExist(err) {
		t.Fatalf("cleanup invented historical process evidence: %v", err)
	}
}

func TestOrcaRecoveryStaleHandleUsesRetainedTree(t *testing.T) {
	for _, childAlive := range []bool{false, true} {
		t.Run(map[bool]string{false: "gone", true: "survivor"}[childAlive], func(t *testing.T) {
			f := newOrcaFake(t)
			h := f.handle(t)
			meta, _ := decodeOrca(h)
			if err := os.WriteFile(meta.PIDFile+".tree", []byte("[123,456]"), 0600); err != nil {
				t.Fatal(err)
			}
			f.alive[123] = false
			f.alive[456] = childAlive
			terminals := []orcaTerminal{}
			f.list = &terminals
			err := New(Orca, f.deps()).Kill(context.Background(), "ben", h)
			if childAlive {
				if err == nil || !strings.Contains(err.Error(), "456") {
					t.Fatalf("surviving child not reported: %v", err)
				}
			} else if err != nil {
				t.Fatalf("gone tree could not recover stale handle: %v", err)
			}
			if f.closed {
				t.Fatal("tried to close an absent handle")
			}
		})
	}
}

func TestOrcaRecoveryBootRetainsTreeAfterAgentExit(t *testing.T) {
	for _, childAlive := range []bool{false, true} {
		t.Run(map[bool]string{false: "gone", true: "survivor"}[childAlive], func(t *testing.T) {
			f := newOrcaFake(t)
			f.alive[456] = true
			h, err := New(Orca, f.deps()).Spawn(context.Background(), SpawnOpts{Session: "r", Slot: "ben", Cwd: f.cwd, Launch: "codex", BootTimeout: 4})
			if err != nil {
				t.Fatal(err)
			}
			f.alive[123], f.alive[456] = false, childAlive
			adapter := New(Orca, f.deps())
			tabs, err := adapter.Capabilities().TabLister.Tabs(context.Background())
			if err != nil || len(tabs) != 1 || tabs[0].Agentless != !childAlive {
				t.Fatalf("dead-root tabs=%+v error=%v", tabs, err)
			}
			agents, err := adapter.Capabilities().Snapshotter.Snapshot(context.Background())
			if err != nil || (len(agents) == 0) != !childAlive {
				t.Fatalf("dead-root agents=%+v error=%v", agents, err)
			}
			err = adapter.Kill(context.Background(), "ben", h)
			if childAlive {
				if err == nil || !strings.Contains(err.Error(), "456") {
					t.Fatalf("survivor lost after root exit: %v", err)
				}
			} else if err != nil {
				t.Fatalf("already-exited agent cannot close: %v", err)
			}
			if !f.closed {
				t.Fatal("retained terminal was not closed")
			}
		})
	}
}

func TestOrcaRecoveryBootTrustDialogRefusesImmediately(t *testing.T) {
	f := newOrcaFake(t)
	f.screens = []string{"Do you trust the contents of this directory?\n› 1. Yes, continue"}
	h, err := New(Orca, f.deps()).Spawn(context.Background(), SpawnOpts{Session: "r", Slot: "ben", Cwd: f.cwd, Launch: "codex", BootTimeout: 60})
	var dialog *DialogError
	if h == "" || !errors.As(err, &dialog) || !errors.Is(err, ErrDialogOpen) {
		t.Fatalf("boot dialog was not returned: handle=%q err=%v", h, err)
	}
	if f.reads != 1 || f.sends != 0 {
		t.Fatalf("dialog waited or received input: reads=%d sends=%d", f.reads, f.sends)
	}
	if !strings.Contains(err.Error(), "Orca terminal") || !strings.Contains(err.Error(), "re-run assign") {
		t.Fatalf("no recovery advice: %v", err)
	}
}

func TestOrcaRecoveryRejectsForeignPIDFile(t *testing.T) {
	f := newOrcaFake(t)
	meta, err := decodeOrca(f.handle(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, pidfile := range []string{filepath.Join(t.TempDir(), "agent-other.pid"), filepath.Join(filepath.Dir(meta.PIDFile), "unrelated.pid")} {
		bad := meta
		bad.PIDFile = pidfile
		if _, err := decodeOrca(encodeOrca(bad)); err == nil {
			t.Fatalf("accepted foreign proof file: %s", pidfile)
		}
	}
}

func TestOrcaRecoveryBusyWordsInBriefDoNotProvePickup(t *testing.T) {
	f := newOrcaFake(t)
	f.receipt = "{}"
	text := "Describe why it is working."
	f.after = text + "\n› "
	err := New(Orca, f.deps()).Send(context.Background(), "ben", f.handle(t), orcaBrief(t, text))
	if !errors.Is(err, ErrNotSubmitted) {
		t.Fatalf("brief echo proved pickup: %v", err)
	}
}

func TestOrcaRecoveryPollingRetainsNewDescendants(t *testing.T) {
	f := newOrcaFake(t)
	deps := f.deps()
	later := false
	deps.Tree = func(pid int) []int {
		if later {
			return []int{pid, 789}
		}
		return []int{pid}
	}
	adapter := New(Orca, deps)
	h, err := adapter.Spawn(context.Background(), SpawnOpts{Session: "r", Slot: "ben", Cwd: f.cwd, Launch: "codex", BootTimeout: 4})
	if err != nil {
		t.Fatal(err)
	}
	later, f.alive[789] = true, true
	if _, err = adapter.Capabilities().PaneReader.ReadPane(context.Background(), "ben", h, 0); err != nil {
		t.Fatal(err)
	}
	f.alive[123] = false
	err = New(Orca, f.deps()).Kill(context.Background(), "ben", h)
	if err == nil || !strings.Contains(err.Error(), "789") {
		t.Fatalf("poll lost later descendant after root exit: %v", err)
	}
}
