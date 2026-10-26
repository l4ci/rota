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

func supersededPRFixture(t *testing.T) (string, Env, *fakeRemote) {
	t.Helper()
	root := newRepo(t, map[string]string{"ben": "ben/12-thing"}, "ben")
	const oldURL = "https://github.com/o/r/pull/7"
	const newURL = "https://github.com/o/r/pull/8"
	writeRegistry(t, root, slot(root, "ben", "ben/12-thing", func(o *jsonx.Object) {
		o.Set("task", "12")
		o.Set("pr", oldURL)
	}))
	fr := &fakeRemote{
		prs:      []tracker.PR{{Number: 8, Branch: "ben/12-thing", URL: newURL}},
		states:   map[int]string{7: "closed"},
		labelled: []int{12},
	}
	return root, env([]host.Agent{}, fr.asForge()), fr
}

func TestReconcileSupersededPR(t *testing.T) {
	root, e, _ := supersededPRFixture(t)
	const oldURL = "https://github.com/o/r/pull/7"
	const newURL = "https://github.com/o/r/pull/8"
	out, err := e.Reconcile(bg, root, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Drift) != 1 || out.Drift[0].Kind != "pr-superseded" || out.Drift[0].Slot != "ben" ||
		!strings.Contains(out.Drift[0].Detail, "#7") || !strings.Contains(out.Drift[0].Detail, "#8") {
		t.Fatalf("want superseded finding naming both PRs, got %+v", out.Drift)
	}
	if got := worker.LoadRegistryTolerant(root).Slot("ben").PR(); got != oldURL {
		t.Fatalf("dry run recorded %q, want %q", got, oldURL)
	}
	out, err = e.Reconcile(bg, root, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Repaired) != 1 || out.Repaired[0].Kind != "pr-superseded" || len(out.Drift) != 0 {
		t.Fatalf("want superseded repair, got %+v", out)
	}
	if got := worker.LoadRegistryTolerant(root).Slot("ben").PR(); got != newURL {
		t.Fatalf("apply recorded %q, want %q", got, newURL)
	}
	out, err = e.Reconcile(bg, root, false)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Clean() {
		t.Fatalf("second reconcile still drifts: %+v", out)
	}
}

func TestReconcileSupersededPRKeepsOtherRecords(t *testing.T) {
	for _, state := range []string{"merged", "open", "same PR", "no replacement"} {
		t.Run(state, func(t *testing.T) {
			root, e, fr := supersededPRFixture(t)
			const oldURL = "https://github.com/o/r/pull/7"
			switch state {
			case "same PR":
				fr.prs[0].Number, fr.prs[0].URL = 7, oldURL
				// The open list already proves this PR is open; no state read is needed.
				fr.stateErrs = map[int]error{7: errors.New("unexpected state read")}
			case "no replacement":
				fr.prs = nil
			default:
				fr.states[7] = state
			}
			out, err := e.Reconcile(bg, root, true)
			if err != nil {
				t.Fatal(err)
			}
			if slices.Contains(kinds(out.Drift)["ben"], "pr-superseded") || len(out.Repaired) != 0 {
				t.Fatalf("unexpected supersession or repair: %+v", out)
			}
			if len(out.Report.Unavailable) != 0 {
				t.Fatalf("unexpected unavailable source: %v", out.Report.Unavailable)
			}
			if got := worker.LoadRegistryTolerant(root).Slot("ben").PR(); got != oldURL {
				t.Fatalf("record changed to %q", got)
			}
		})
	}
}

func TestReconcileSupersededPRForgeUnreadable(t *testing.T) {
	for _, read := range []string{"open PRs", "recorded PR state", "labels"} {
		t.Run(read, func(t *testing.T) {
			root, e, fr := supersededPRFixture(t)
			switch read {
			case "open PRs":
				fr.prsErr = errors.New("forge down")
			case "recorded PR state":
				fr.stateErrs = map[int]error{7: errors.New("forge down")}
			case "labels":
				e.Forge = closedListFails{e.Forge}
			}
			out, err := e.Reconcile(bg, root, true)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(out.Report.Unavailable, SourceForge) || len(out.Repaired) != 0 {
				t.Fatalf("unreadable forge must be reported without repair: %+v", out)
			}
			if got := worker.LoadRegistryTolerant(root).Slot("ben").PR(); got != "https://github.com/o/r/pull/7" {
				t.Fatalf("unreadable forge changed record to %q", got)
			}
			if read == "labels" && !slices.Contains(kinds(out.Drift)["ben"], "pr-superseded") {
				t.Fatalf("partly unreadable forge must leave supersession in drift: %+v", out.Drift)
			}
		})
	}
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
	if k := kinds(out.Drift)["ext-1"]; len(k) != 1 || k[0] != MergedExternal || worker.LoadRegistryTolerant(root).Slot("ext-1") == nil {
		t.Fatalf("report-only: drift %v, slot must stay", k)
	}
	out, err = e.Reconcile(bg, root, true)
	if err != nil {
		t.Fatal(err)
	}
	if k := kinds(out.Repaired)["ext-1"]; len(k) != 1 || k[0] != MergedExternal {
		t.Fatalf("apply should repair merged-external: repaired %v drift %v warnings %v", out.Repaired, out.Drift, out.Report.Warnings)
	}
	if worker.LoadRegistryTolerant(root).Slot("ext-1") != nil {
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
	e.Round.ItemTimeoutMinutes, e.Round.StallMinutes = 1, 1
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
	if worker.LoadRegistryTolerant(root).Slot("ext-1") == nil {
		t.Error("slot released on a forge read that failed")
	}
	if !slices.ContainsFunc(out.Report.Warnings, func(w string) bool { return strings.Contains(w, "skipped") && strings.Contains(w, "forge") }) {
		t.Errorf("want a skipped-repair warning: %v", out.Report.Warnings)
	}
}

// finnDead drops finn's live agent so the host reports a dead tab beside
// finn's PR.
func finnDead(t *testing.T, state string) (string, Env) {
	root, e, forge := fixture(t)
	forge.states[9] = state
	snap := e.Snapshot
	e.Snapshot = func(ctx context.Context) ([]host.Agent, error) {
		all, err := snap(ctx)
		return slices.DeleteFunc(all, func(a host.Agent) bool { return a.Name == "finn" }), err
	}
	return root, e
}

// A dead tab and a merged PR on one slot: the dead-tab repair marks it dead,
// and the park must still follow in the same run.
func TestReconcileApplyParksADeadSlotWhoseMergedPR(t *testing.T) {
	root, e := finnDead(t, "merged")
	out, err := e.Reconcile(bg, root, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := kinds(out.Repaired)["finn"]; !slices.Equal(got, []string{DeadTab, PRStale}) {
		t.Errorf("finn repaired = %v, warnings %v", got, out.Report.Warnings)
	}
	if got := kinds(out.Drift)["finn"]; len(got) != 0 {
		t.Errorf("finn still drifts: %v", got)
	}
	if f := worker.LoadRegistryTolerant(root).Slot("finn"); f.Branch() != "park/finn" || f.PR() != "" || f.State() != "idle" {
		t.Errorf("finn not parked: branch %q pr %q state %q", f.Branch(), f.PR(), f.State())
	}
}

// A closed PR may hold unmerged work: the slot is marked dead and stays put.
func TestReconcileApplyKeepsADeadSlotWhoseClosedPR(t *testing.T) {
	root, e := finnDead(t, "closed")
	out, err := e.Reconcile(bg, root, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := kinds(out.Repaired)["finn"]; !slices.Equal(got, []string{DeadTab}) {
		t.Errorf("finn repaired = %v", got)
	}
	f := worker.LoadRegistryTolerant(root).Slot("finn")
	if f.State() != "dead" || f.Branch() != "finn/57-baz" {
		t.Errorf("finn = state %q branch %q", f.State(), f.Branch())
	}
}

// A slot marked dead by an earlier run (a tick) is parked once its PR merges.
func TestReconcileApplyParksAnEarlierDeadSlotWhoseMergedPR(t *testing.T) {
	root, e := finnDead(t, "merged")
	if _, err := worker.UpdateSlot(root, "finn", func(s *worker.Slot) { s.SetHandle(""); _ = s.MarkState("dead", "") }); err != nil {
		t.Fatal(err)
	}
	out, err := e.Reconcile(bg, root, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := kinds(out.Repaired)["finn"]; !slices.Equal(got, []string{PRStale}) {
		t.Errorf("finn repaired = %v, warnings %v", got, out.Report.Warnings)
	}
	if f := worker.LoadRegistryTolerant(root).Slot("finn"); f.Branch() != "park/finn" || f.State() != "idle" {
		t.Errorf("finn not parked: branch %q state %q", f.Branch(), f.State())
	}
}

// Dead plus a merged PR still refuses a dirty worktree.
func TestReconcileApplyKeepsADeadDirtySlotWhoseMergedPR(t *testing.T) {
	root, e := finnDead(t, "merged")
	if err := os.WriteFile(filepath.Join(root, ".worktrees", "finn", "wip.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := e.Reconcile(bg, root, true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(kinds(out.Drift)["finn"], PRStale) || !slices.ContainsFunc(out.Report.Warnings, func(w string) bool { return strings.Contains(w, "uncommitted") }) {
		t.Errorf("want pr-stale drift and an uncommitted warning: drift %v warnings %v", out.Drift, out.Report.Warnings)
	}
	if f := worker.LoadRegistryTolerant(root).Slot("finn"); f.Branch() != "finn/57-baz" {
		t.Errorf("dirty slot was parked: %q", f.Branch())
	}
}

// A relay can leave a slot blocked after its PR merged: it is parked like a
// done one.
func TestReconcileApplyParksABlockedSlotWhoseMergedPR(t *testing.T) {
	root, e := finnDead(t, "merged")
	if _, err := worker.UpdateSlot(root, "finn", func(s *worker.Slot) { s.SetHandle(""); _ = s.MarkState("blocked", "") }); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Reconcile(bg, root, true); err != nil {
		t.Fatal(err)
	}
	if f := worker.LoadRegistryTolerant(root).Slot("finn"); f.Branch() != "park/finn" || f.State() != "idle" {
		t.Errorf("finn not parked: branch %q state %q", f.Branch(), f.State())
	}
}

// A blocked slot whose PR is open or closed unmerged is not freed: the park
// repair is offered for a merged PR only.
func TestReconcileApplyKeepsABlockedSlotWhoseUnmergedPR(t *testing.T) {
	for _, state := range []string{"open", "closed"} {
		t.Run(state, func(t *testing.T) {
			root, e := finnDead(t, state)
			if _, err := worker.UpdateSlot(root, "finn", func(s *worker.Slot) { s.SetHandle(""); _ = s.MarkState("blocked", "") }); err != nil {
				t.Fatal(err)
			}
			out, err := e.Reconcile(bg, root, true)
			if err != nil {
				t.Fatal(err)
			}
			if slices.Contains(kinds(out.Repaired)["finn"], PRStale) {
				t.Errorf("pr-stale repaired for a %s PR: %v", state, out.Repaired)
			}
			if f := worker.LoadRegistryTolerant(root).Slot("finn"); f.Branch() != "finn/57-baz" || f.State() != "blocked" {
				t.Errorf("finn freed: branch %q state %q", f.Branch(), f.State())
			}
		})
	}
}

// movedPane has kit's pane moved into the orchestrator's tab: the agent keeps
// the name it was started under, but its tab is no longer the recorded one.
func movedPane(t *testing.T) (string, Env, *[]string) {
	root, e, _ := fixture(t)
	snap := e.Snapshot
	e.Snapshot = func(ctx context.Context) ([]host.Agent, error) {
		all, err := snap(ctx)
		for i := range all {
			if all[i].Name == "kit" {
				all[i].Name, all[i].Tab = host.AgentName("kit", "w1:t3"), "w4:t9"
			}
		}
		return all, err
	}
	var renamed []string
	e.FollowTab = func(_ context.Context, slot, from, to string) error {
		renamed = append(renamed, slot+" "+from+" "+to)
		return nil
	}
	return root, e, &renamed
}

func TestStatusReportsAPaneMovedToAnotherTab(t *testing.T) {
	root, e, _ := movedPane(t)
	rep, err := e.Status(bg, root)
	if err != nil {
		t.Fatal(err)
	}
	if got := kinds(rep.Findings)["kit"]; !slices.Contains(got, TabMoved) || slices.Contains(got, DeadTab) {
		t.Fatalf("kit findings = %v, want tab-moved and no dead-tab", got)
	}
	for _, f := range rep.Findings {
		if f.Kind == TabMoved && (!strings.Contains(f.Detail, "w1:t3") || !strings.Contains(f.Detail, "w4:t9")) {
			t.Errorf("detail %q must name the recorded and the current tab", f.Detail)
		}
	}
}

func TestReconcileApplyRecordsTheMovedPanesTab(t *testing.T) {
	root, e, renamed := movedPane(t)
	out, err := e.Reconcile(bg, root, true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(kinds(out.Repaired)["kit"], TabMoved) {
		t.Fatalf("kit repaired = %v, warnings %v", kinds(out.Repaired)["kit"], out.Report.Warnings)
	}
	if got := worker.LoadRegistryTolerant(root).Slot("kit").Handle(); got != "w4:t9" {
		t.Errorf("kit handle = %q, want w4:t9", got)
	}
	if want := []string{"kit w1:t3 w4:t9"}; !slices.Equal(*renamed, want) {
		t.Errorf("renames = %v, want %v", *renamed, want)
	}
	// The agent now carries the name of its new tab, as herdr would report it.
	snap := e.Snapshot
	e.Snapshot = func(ctx context.Context) ([]host.Agent, error) {
		all, err := snap(ctx)
		for i := range all {
			if all[i].Tab == "w4:t9" {
				all[i].Name = host.AgentName("kit", "w4:t9")
			}
		}
		return all, err
	}
	again, err := e.Reconcile(bg, root, false)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(kinds(again.Drift)["kit"], TabMoved) {
		t.Errorf("a second reconcile still drifts: %v", again.Drift)
	}
}

func TestReconcileApplyKeepsTheHandleWhenTheRenameFails(t *testing.T) {
	root, e, _ := movedPane(t)
	e.FollowTab = func(context.Context, string, string, string) error { return errors.New("agent_not_found") }
	out, err := e.Reconcile(bg, root, true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(kinds(out.Drift)["kit"], TabMoved) {
		t.Errorf("kit drift = %v, want tab-moved still reported", kinds(out.Drift)["kit"])
	}
	if got := worker.LoadRegistryTolerant(root).Slot("kit").Handle(); got != "w1:t3" {
		t.Errorf("kit handle = %q, want it untouched so the agent name still resolves", got)
	}
}
