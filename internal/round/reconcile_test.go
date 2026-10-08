package round

import (
	"context"
	"errors"
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

func TestStatusExternalMergedPRLeavesTheList(t *testing.T) {
	root, e, fr := extFixture(t, "https://github.com/o/r/pull/7")
	fr.states[7] = "merged"
	rep, err := e.Status(bg, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rowOf(rep, "ext-1"); ok {
		t.Errorf("merged external slot still listed: %+v", rep.Rows)
	}
	if k := kinds(rep.Findings)["ext-1"]; len(k) != 0 {
		t.Errorf("findings for a merged external slot: %v", k)
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
