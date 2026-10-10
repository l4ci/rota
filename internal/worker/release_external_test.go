package worker

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/exitcode"
)

// adoptedWorld is a gate world whose only slot is the adopted ext-1 on the
// branch w1 (PR 7), with w1 checked out in a real worktree of the gate repo.
func adoptedWorld(t *testing.T, body string) (*world, string) {
	t.Helper()
	w := newWorld(t, "")
	wt := filepath.Join(filepath.Dir(w.dir), "ext-wt")
	gitq(t, w.dir, "fetch", "-q", "origin")
	gitq(t, w.dir, "worktree", "add", "-q", "-b", "w1", wt, "origin/w1")
	if err := os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"), []byte(`{"slots":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RegisterExternal(w.dir, "ext-1", "w1", wt, "main", "", ghURL); err != nil {
		t.Fatal(err)
	}
	w.forge("body", body)
	return w, wt
}

func (w *world) gateExt(o GateOpts) (GateResult, error) {
	o.Slot, o.Base = "ext-1", "main"
	return w.env(false).Gate(bg, w.dir, o)
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestGateAdoptedSlotProvenance(t *testing.T) {
	w, _ := adoptedWorld(t, "Closes #5\n\n## Approvals\nNone\n")
	tg, err := LoadRegistryTolerant(w.dir).GateTarget("ext-1")
	if err != nil {
		t.Fatal(err)
	}
	if !tg.External || tg.Name != "ext-1" || len(tg.relays) != 0 {
		t.Errorf("target of an adopted slot: %+v", tg)
	}
	res, err := w.gateExt(GateOpts{CheckOnly: true})
	if err != nil || res.Verdict != GateFresh {
		t.Fatalf("verdict %q (%v): %+v", res.Verdict, err, res)
	}
}

func TestGateReleasesAnAdoptedSlotAndKeepsItsCheckout(t *testing.T) {
	w, wt := adoptedWorld(t, "## Approvals\nNone\n")
	res, err := w.gateExt(GateOpts{})
	if err != nil || res.Verdict != GatePass {
		t.Fatalf("verdict %q (%v): %+v", res.Verdict, err, res)
	}
	if LoadRegistryTolerant(w.dir).Slot("ext-1") != nil {
		t.Error("the slot is still registered after its PR merged")
	}
	if !exists(wt) || gitq(t, w.dir, "branch", "--list", "w1") == "" {
		t.Error("a gate pass without --prune removed the worktree or branch")
	}
}

func TestGateCheckOnlyKeepsAnAdoptedSlot(t *testing.T) {
	w, _ := adoptedWorld(t, "")
	if _, err := w.gateExt(GateOpts{CheckOnly: true}); err != nil {
		t.Fatal(err)
	}
	if LoadRegistryTolerant(w.dir).Slot("ext-1") == nil {
		t.Error("check-only released the slot")
	}
}

func TestGatePruneRemovesAnAdoptedCheckout(t *testing.T) {
	w, wt := adoptedWorld(t, "")
	res, err := w.gateExt(GateOpts{Prune: true})
	if err != nil || res.Verdict != GatePass {
		t.Fatalf("verdict %q (%v)", res.Verdict, err)
	}
	if exists(wt) || gitq(t, w.dir, "branch", "--list", "w1") != "" {
		t.Error("--prune left the worktree or branch")
	}
}

func TestReleaseExternalKeepsWorktree(t *testing.T) {
	w, wt := adoptedWorld(t, "")
	if err := w.env(false).ReleaseExternal(w.dir, "ext-1", false); err != nil {
		t.Fatal(err)
	}
	if LoadRegistryTolerant(w.dir).Slot("ext-1") != nil {
		t.Error("slot still registered")
	}
	if !exists(wt) {
		t.Error("release deleted the worktree without --prune")
	}
	if gitq(t, w.dir, "branch", "--list", "w1") == "" {
		t.Error("release deleted the branch without --prune")
	}
}

func TestReleaseExternalPruneRefusesADirtyWorktree(t *testing.T) {
	w, wt := adoptedWorld(t, "")
	os.WriteFile(filepath.Join(wt, "wip.txt"), []byte("x"), 0o644)
	err := w.env(false).ReleaseExternal(w.dir, "ext-1", true)
	if err == nil {
		t.Fatal("pruned a dirty worktree")
	}
	if !exists(wt) || !exists(filepath.Join(wt, "wip.txt")) {
		t.Error("dirty worktree was deleted")
	}
}

func TestReleaseExternalRefusesARoundSlot(t *testing.T) {
	w := newWorld(t, "")
	err := w.env(false).ReleaseExternal(w.dir, "w1", true)
	var xe *exitcode.Error
	if !errors.As(err, &xe) || xe.Exit != exitcode.ExitRefused {
		t.Fatalf("err = %v", err)
	}
	if LoadRegistryTolerant(w.dir).Slot("w1") == nil {
		t.Error("a round slot was released")
	}
}
