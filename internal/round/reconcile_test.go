package round

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
	tip := gitIn(t, root, "rev-parse", "codex/12-thing")
	fr.mergedPRs = map[string][]tracker.PR{"codex/12-thing": {{Number: 7, Branch: "codex/12-thing", URL: "https://github.com/o/r/pull/7", HeadSHA: tip}}}
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

// A branch name reused after an older PR merged: that PR's head is not on the
// live branch, so it is not this slot's PR and the slot must not read merged.
func TestStatusExternalOlderMergedPROnTheSameBranchNameIsNotOurs(t *testing.T) {
	root, e, fr := extFixture(t, "")
	oldHead := strings.Repeat("a", 40)
	fr.mergedPRs = map[string][]tracker.PR{"codex/12-thing": {{Number: 3, Branch: "codex/12-thing", URL: "https://github.com/o/r/pull/3", HeadSHA: oldHead}}}
	fr.states[3] = "merged"
	rep, err := e.Status(bg, root)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := rowOf(rep, "ext-1")
	if r.State == "merged" || r.PRState == "merged" || r.PR != "" {
		t.Fatalf("an older PR on a reused branch name marked the live slot merged: %+v", r)
	}
	if k := kinds(rep.Findings)["ext-1"]; slices.Contains(k, MergedExternal) {
		t.Errorf("no merged-external finding expected, got %v", k)
	}
}

// A branch recreated from main under an old name contains the old PR's head
// (rota merges with merge commits, so that head is reachable from main): being
// on the branch proves nothing, and the live slot must not read merged.
func TestStatusExternalOldMergedPRReachableThroughBaseIsNotOurs(t *testing.T) {
	root, e, fr := extFixture(t, "")
	oldHead := gitIn(t, root, "rev-parse", "main")
	fr.mergedPRs = map[string][]tracker.PR{"codex/12-thing": {{Number: 3, Branch: "codex/12-thing", URL: "https://github.com/o/r/pull/3", HeadSHA: oldHead}}}
	fr.states[3] = "merged"
	rep, err := e.Status(bg, root)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := rowOf(rep, "ext-1")
	if r.State == "merged" || r.PRState == "merged" || r.PR != "" {
		t.Fatalf("an old PR reachable through the base marked the live slot merged: %+v", r)
	}
	if k := kinds(rep.Findings)["ext-1"]; slices.Contains(k, MergedExternal) {
		t.Errorf("no merged-external finding expected, got %v", k)
	}
}

// Status never fetches, so origin/main can run ahead of local main. A branch
// recreated from origin/main holds the old PR head though local main does not.
func TestStatusExternalOldMergedPRReachableThroughOriginBaseIsNotOurs(t *testing.T) {
	root, e, fr := extFixture(t, "")
	tree := gitIn(t, root, "rev-parse", "main^{tree}")
	x := gitIn(t, root, "-c", "user.name=t", "-c", "user.email=t@t", "commit-tree", tree, "-p", "main", "-m", "merged upstream")
	live := gitIn(t, root, "-c", "user.name=t", "-c", "user.email=t@t", "commit-tree", tree, "-p", x, "-m", "live work")
	gitIn(t, root, "update-ref", "refs/remotes/origin/main", x)
	gitIn(t, root, "update-ref", "refs/heads/codex/12-thing", live)
	fr.mergedPRs = map[string][]tracker.PR{"codex/12-thing": {{Number: 3, Branch: "codex/12-thing", URL: "https://github.com/o/r/pull/3", HeadSHA: x}}}
	fr.states[3] = "merged"
	rep, err := e.Status(bg, root)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := rowOf(rep, "ext-1")
	if r.State == "merged" || r.PRState == "merged" || r.PR != "" {
		t.Fatalf("a PR reachable through origin/base marked the live slot merged: %+v", r)
	}
}

// A forge that cannot list merged PRs leaves the row unknown-safe: a warning,
// no PR attached and no release.
func TestStatusExternalMergedLookupErrorWarnsAndKeepsTheSlot(t *testing.T) {
	root, e, fr := extFixture(t, "")
	fr.mergedErr = errors.New("forge down")
	rep, err := e.Status(bg, root)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := rowOf(rep, "ext-1")
	if r.PR != "" || r.State == "merged" {
		t.Errorf("row %+v", r)
	}
	if !slices.ContainsFunc(rep.Warnings, func(w string) bool {
		return strings.Contains(w, "merged PRs of codex/12-thing") && strings.Contains(w, "forge down")
	}) {
		t.Errorf("want a merged-PR warning, got %v", rep.Warnings)
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

func TestUnreadSourceGatesRepairs(t *testing.T) {
	rep := &Report{Unavailable: []string{SourceHost}}
	if got := unreadSource(rep, DeadTab); got != SourceHost {
		t.Errorf("dead-tab with host down = %q", got)
	}
	if got := unreadSource(rep, PRStale); got != "" {
		t.Errorf("pr-stale needs the forge, which answered: %q", got)
	}
	rep = &Report{Unavailable: []string{SourceForge}}
	for _, k := range []string{PRUnrecorded, MergedExternal, PRStale, LabelMissing, LabelStale} {
		if got := unreadSource(rep, k); got != SourceForge {
			t.Errorf("%s with forge down = %q", k, got)
		}
	}
	if got := unreadSource(rep, DeadTab); got != "" {
		t.Errorf("dead-tab does not read the forge: %q", got)
	}
}

// closedListFails reads the open lists fine and fails the closed-issue list,
// so a finding is computed from one forge call while another one failed.
type closedListFails struct{ Forge }

func (c closedListFails) List(ctx context.Context, f tracker.ListFilter) ([]tracker.Issue, error) {
	if f.State == "closed" {
		return nil, errors.New("closed list flaked")
	}
	return c.Forge.List(ctx, f)
}

// A finding computed while the forge was partly unreadable is a guess: apply
// must warn and leave the slot registered (#579).
func TestReconcileApplySkipsARepairWhoseSourceWasUnreadable(t *testing.T) {
	root, e, fr := extFixture(t, "https://github.com/o/r/pull/7")
	fr.states[7] = "merged"
	e.Forge = closedListFails{e.Forge}
	out, err := e.Reconcile(bg, root, true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(out.Report.Unavailable, SourceForge) {
		t.Fatalf("fixture must leave the forge unavailable: %v", out.Report.Unavailable)
	}
	if k := kinds(out.Drift)["ext-1"]; len(k) != 1 || k[0] != MergedExternal {
		t.Fatalf("the merged-external finding must stay in drift: drift %v repaired %v", out.Drift, out.Repaired)
	}
	if worker.LoadRegistry(root).Slot("ext-1") == nil {
		t.Error("slot released on a forge read that failed")
	}
	if !slices.ContainsFunc(out.Report.Warnings, func(w string) bool { return strings.Contains(w, "skipped") && strings.Contains(w, "forge") }) {
		t.Errorf("want a skipped-repair warning: %v", out.Report.Warnings)
	}
}
