package round

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// extFixture is one adopted slot on a branch with work on it, plus a
// non-external control, under a host that lists no agents at all.
func extFixture(t *testing.T, pr string, prs ...tracker.PR) (string, Env, *fakeRemote) {
	t.Helper()
	root := newRepo(t, map[string]string{"ext-1": "codex/12-thing", "ben": "park/ben"}, "ext-1")
	writeRegistry(t, root,
		slot(root, "ext-1", "codex/12-thing", func(o *jsonx.Object) {
			o.Set("kind", worker.KindExternal)
			o.Set("task", "12")
			o.Set("handle", "")
			if pr != "" {
				o.Set("pr", pr)
			}
		}),
		slot(root, "ben", "park/ben", nil),
	)
	fr := &fakeRemote{prs: prs, states: map[int]string{}, labelled: []int{12}}
	return root, env([]host.Agent{}, fr.asForge()), fr
}

func rowOf(rep *Report, name string) (Row, bool) {
	for _, r := range rep.Rows {
		if r.Name == name {
			return r, true
		}
	}
	return Row{}, false
}

func TestStatusExternalDerivedState(t *testing.T) {
	url := "https://github.com/o/r/pull/7"
	root, e, _ := extFixture(t, "", tracker.PR{Number: 7, Branch: "codex/12-thing", URL: url})
	rep, err := e.Status(bg, root)
	if err != nil {
		t.Fatal(err)
	}
	r, ok := rowOf(rep, "ext-1")
	if !ok || r.HostState != "external" || r.State != "done" || r.PRState != "open" || r.Kind != "" {
		t.Fatalf("open PR: %+v", r)
	}

	// Branch ahead of base, no PR: busy.
	root, e, _ = extFixture(t, "")
	rep, _ = e.Status(bg, root)
	if r, _ := rowOf(rep, "ext-1"); r.HostState != "external" || r.State != "busy" {
		t.Errorf("ahead, no PR: %+v", r)
	}
	// A non-external row carries no derived state.
	if r, _ := rowOf(rep, "ben"); r.State != "" || r.HostState == "external" {
		t.Errorf("control: %+v", r)
	}
}

func TestStatusExternalMergedPRStaysListedUntilReleased(t *testing.T) {
	root, e, fr := extFixture(t, "https://github.com/o/r/pull/7")
	fr.states[7] = "merged"
	rep, err := e.Status(bg, root)
	if err != nil {
		t.Fatal(err)
	}
	r, ok := rowOf(rep, "ext-1")
	if !ok || r.State != "merged" || r.PRState != "merged" {
		t.Fatalf("a merged external slot stays listed as merged: %+v", rep.Rows)
	}
	if k := kinds(rep.Findings)["ext-1"]; len(k) != 1 || k[0] != MergedExternal {
		t.Errorf("want one merged-external finding, got %v", k)
	}
}

// A PR that opened and merged between two passes was never in the open list, so
// an adopted slot that recorded none is matched to it by branch.
func TestStatusExternalMergedPRFoundByBranchWhenNoneRecorded(t *testing.T) {
	root, e, fr := extFixture(t, "")
	fr.mergedPRs = map[string][]tracker.PR{"codex/12-thing": {{Number: 7, Branch: "codex/12-thing", URL: "https://github.com/o/r/pull/7"}}}
	fr.states[7] = "merged"
	rep, err := e.Status(bg, root)
	if err != nil {
		t.Fatal(err)
	}
	r, ok := rowOf(rep, "ext-1")
	if !ok || r.State != "merged" || r.PRState != "merged" || r.PR != "https://github.com/o/r/pull/7" {
		t.Fatalf("a merge outside the gate should be found by branch: %+v", r)
	}
	if k := kinds(rep.Findings)["ext-1"]; len(k) != 1 || k[0] != MergedExternal {
		t.Errorf("want one merged-external finding, got %v", k)
	}
	if r, _ := rowOf(rep, "ben"); r.PRState != "" {
		t.Errorf("the control slot must not be looked up: %+v", r)
	}
}

func TestReconcileApplyReleasesAMergedExternalSlot(t *testing.T) {
	root, e, fr := extFixture(t, "https://github.com/o/r/pull/7")
	fr.states[7] = "merged"
	out, err := e.Reconcile(bg, root, false)
	if err != nil {
		t.Fatal(err)
	}
	if k := kinds(out.Drift)["ext-1"]; len(k) != 1 || k[0] != MergedExternal || worker.LoadRegistry(root).Slot("ext-1") == nil {
		t.Fatalf("report-only: drift %v, slot must stay", k)
	}
	out, err = e.Reconcile(bg, root, true)
	if err != nil {
		t.Fatal(err)
	}
	if k := kinds(out.Repaired)["ext-1"]; len(k) != 1 || k[0] != MergedExternal {
		t.Fatalf("apply should repair merged-external: repaired %v drift %v warnings %v", out.Repaired, out.Drift, out.Report.Warnings)
	}
	if worker.LoadRegistry(root).Slot("ext-1") != nil {
		t.Error("slot still registered after apply")
	}
	if _, err := os.Stat(filepath.Join(root, ".worktrees", "ext-1")); err != nil {
		t.Errorf("release must keep the worktree: %v", err)
	}
	if gitIn(t, root, "branch", "--list", "codex/12-thing") == "" {
		t.Error("release must keep the branch")
	}
}

func TestStatusExternalOnePRReadFailingIsUnknown(t *testing.T) {
	root, e, fr := extFixture(t, "https://github.com/o/r/pull/7")
	fr.stateErrs = map[int]error{7: errors.New("boom")}
	rep, err := e.Status(bg, root)
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := rowOf(rep, "ext-1"); !ok || r.State != "unknown" {
		t.Errorf("one failed PR read must derive unknown, not busy/idle: %+v", r)
	}
	if r, ok := rowOf(rep, "ben"); !ok || r.State != "" {
		t.Errorf("the control row is untouched: %+v", r)
	}
}

func TestStatusExternalForgeDown(t *testing.T) {
	root, e, fr := extFixture(t, "")
	fr.prsErr = errors.New("forge down")
	rep, err := e.Status(bg, root)
	if err != nil {
		t.Fatalf("a forge outage must not fail status: %v", err)
	}
	r, ok := rowOf(rep, "ext-1")
	if !ok || r.State != "unknown" || r.HostState != "external" {
		t.Fatalf("forge down: %+v", r)
	}
	if _, ok := rowOf(rep, "ben"); !ok {
		t.Error("the round must continue past the outage")
	}
}

func TestReconcileSkipsExternal(t *testing.T) {
	root, e, _ := extFixture(t, "https://github.com/o/r/pull/7", tracker.PR{Number: 7, Branch: "codex/12-thing", URL: "https://github.com/o/r/pull/7"})
	// A stale handle and an old activity stamp would read DeadTab, StalledSlot
	// and ItemTimeout on a driven slot.
	if err := rawSlot(root, "ext-1", func(o *jsonx.Object) {
		o.Set("handle", "w9:t9")
		o.Set("activeAt", "2020-01-01T00:00:00Z")
	}); err != nil {
		t.Fatal(err)
	}
	if err := worker.RecordItemStart(root, "12", time.Now().Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	e.ItemTimeoutMinutes, e.StallMinutes = 1, 1
	e.Snapshot = func(context.Context) ([]host.Agent, error) {
		return []host.Agent{{Tab: "w9:t9", Name: "ext-1", Cwd: filepath.Join(root, ".worktrees", "ext-1"), Status: "idle"}}, nil
	}
	rep, err := e.Status(bg, root)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range rep.Findings {
		if f.Slot == "ext-1" && (f.Kind == DeadTab || f.Kind == UnclaimedTab || f.Kind == StalledSlot || f.Kind == ItemTimeout) {
			t.Errorf("host finding on an external slot: %+v", f)
		}
	}
	e.Snapshot = func(context.Context) ([]host.Agent, error) { return []host.Agent{}, nil }
	rep, _ = e.Status(bg, root)
	for _, f := range rep.Findings {
		if f.Slot == "ext-1" && (f.Kind == DeadTab || f.Kind == ItemTimeout) {
			t.Errorf("no agent: %+v", f)
		}
	}
}
