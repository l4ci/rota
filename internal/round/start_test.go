package round

import (
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/itembody"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/roundlease"
	"github.com/l4ci/rota/internal/worker"
)

func milestoneDoc(t *testing.T, root, id, status string, depends ...string) {
	t.Helper()
	dir := filepath.Join(root, ".rota", "milestones")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	dep := "[]"
	if len(depends) > 0 {
		dep = "[" + strings.Join(depends, ", ") + "]"
	}
	doc := fmt.Sprintf("---\nid: %s\ntitle: \"%s\"\nstatus: %s\ndepends: %s\n---\n\n# %s\n", id, id, status, dep, id)
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
}

func ids(cs []Candidate) []string {
	out := []string{}
	for _, c := range cs {
		out = append(out, c.ID)
	}
	return out
}

func TestDependencies(t *testing.T) {
	body := "## Goal\nx\n\n## Depends on\n\n- #57 (C1)\n- #58\n- owner/repo#9\n- https://example.com/x/1\n- B07\n\n## Acceptance\n- #99 is not a dependency\n"
	refs, bad := Dependencies(body)
	if !reflect.DeepEqual(refs, []string{"57", "58", "B07"}) {
		t.Errorf("refs %v", refs)
	}
	if !reflect.DeepEqual(bad, []string{"owner/repo#9", "https://example.com/x/1"}) {
		t.Errorf("unverifiable %v", bad)
	}
	if r, b := Dependencies("no section here #5"); r != nil || b != nil {
		t.Errorf("no section means no dependencies: %v %v", r, b)
	}
}

var tracked = []string{"internal/cli/tree.go", "internal/cli/round.go", "internal/worker/pool.go", "docs/contributing/contract/README.md", "README.md"}

func TestFootprint(t *testing.T) {
	text := "Touches `internal/cli/round.go` and internal/worker/. Also docs/ (too coarse), README.md and made/up/path.go."
	got := Footprint(text, tracked, nil)
	want := []string{"README.md", "internal/cli/round.go", "internal/worker/"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("inferred %v want %v", got, want)
	}
	// An explicit ## Files section wins over inference, and shared paths drop out.
	explicit := "mentions internal/cli/tree.go in prose\n\n## Files\n- `internal/worker/pool.go`\n- docs/contributing/contract/README.md\n- internal/new/*.go\n"
	got = Footprint(explicit, tracked, []string{"docs/contributing/contract/README.md"})
	if !reflect.DeepEqual(got, []string{"internal/new/*.go", "internal/worker/pool.go"}) {
		t.Errorf("explicit %v", got)
	}
}

func TestOverlaps(t *testing.T) {
	cases := []struct {
		a, b []string
		want []string
	}{
		{[]string{"a/b.go"}, []string{"a/b.go"}, []string{"a/b.go"}},
		{[]string{"a/"}, []string{"a/b.go"}, []string{"a/b.go"}},
		{[]string{"a/b.go"}, []string{"a/"}, []string{"a/b.go"}},
		{[]string{"internal/new/*.go"}, []string{"internal/new/x.go"}, []string{"internal/new/x.go"}},
		{[]string{"a/b.go"}, []string{"a/c.go"}, nil},
		{[]string{"a/"}, []string{"ab/c.go"}, nil},
	}
	for _, c := range cases {
		if got := Overlaps(c.a, c.b); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%v vs %v: %v want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestAssessChecks(t *testing.T) {
	be := &fakeRemote{ready: map[string][]string{"3": {"no acceptance criteria in the issue body", "no design or plan note"}}}
	be.add("1", "done dep", "", true, "")
	be.add("2", "open dep", "", false, "")
	be.add("3", "no criteria", "", false, "")
	be.add("4", "ok", "", false, "## Depends on\n- #1\n")
	be.add("5", "open dependency", "", false, "## Depends on\n- #2\n")
	be.add("6", "foreign dependency", "", false, "## Depends on\n- o/r#3\n- #404\n")
	be.add("7", "edits the pool", "", false, "changes internal/worker/pool.go")
	be.add("8", "also edits the pool", "", false, "changes internal/worker/pool.go")

	if r, err := Assess(be, "4", tracked, nil, nil, false); err != nil || !r.Ready() {
		t.Fatalf("4 must be ready: %v %+v", err, r)
	}
	r, _ := Assess(be, "3", tracked, nil, nil, false)
	if r.Ready() || r.Checks[0].Name != CheckCriteria || r.Checks[0].OK || len(r.Checks[0].Detail) != 2 {
		t.Errorf("3 lacks criteria: %+v", r)
	}
	r, _ = Assess(be, "5", tracked, nil, nil, false)
	if r.Ready() || !strings.Contains(strings.Join(r.Checks[1].Detail, ";"), "2 is not done") {
		t.Errorf("5 has an open dependency: %+v", r)
	}
	r, _ = Assess(be, "6", tracked, nil, nil, false)
	d := strings.Join(r.Checks[1].Detail, ";")
	if r.Ready() || !strings.Contains(d, "cannot verify o/r#3") || !strings.Contains(d, "cannot verify 404") || !strings.Contains(d, "edit the issue") {
		t.Errorf("6 must fail closed and say how to fix it: %q", d)
	}

	flight := []InFlight{{Slot: "ben", Issue: "7", Paths: []string{"internal/worker/pool.go"}}}
	r, _ = Assess(be, "8", tracked, nil, flight, false)
	if r.Ready() || len(r.Overlaps) != 1 || r.Overlaps[0].With != "7" || r.Overlaps[0].Slot != "ben" {
		t.Errorf("8 overlaps 7: %+v", r)
	}
	r, _ = Assess(be, "8", tracked, nil, flight, true)
	if !r.Ready() || len(r.Overlaps) != 1 {
		t.Errorf("accepted overlap stays listed but does not fail: %+v", r)
	}
	r, _ = Assess(be, "8", tracked, []string{"internal/worker/*.go"}, flight, false)
	if !r.Ready() {
		t.Errorf("shared paths are ignored: %+v", r)
	}
	if _, err := Assess(be, "99", tracked, nil, nil, false); !errors.Is(err, backlog.ErrNotFound) {
		t.Errorf("unknown item: %v", err)
	}
}

func TestCandidatesPerScope(t *testing.T) {
	root := newRepo(t, nil)
	milestoneDoc(t, root, "M01", "active")
	milestoneDoc(t, root, "M02", "planned", "M01")
	milestoneDoc(t, root, "M03", "planned", "M09")
	be := &fakeRemote{}
	be.add("10", "in M01", "M01", false, "")
	be.add("11", "in M01, held", "M01", false, "")
	be.add("12", "in M02", "M02", false, "")
	be.add("13", "in M03", "M03", false, "")
	be.add("14", "untagged", "", false, "")
	be.add("15", "closed in M01", "M01", true, "")
	writeRegistry(t, root, slot(root, "ben", "ben/11-held", nil))
	e := Env{Git: git.Exec, Base: "main"}
	get := func(o CandidateOpts) []string {
		t.Helper()
		cs, err := e.Candidates(bg, root, be, o)
		if err != nil {
			t.Fatal(err)
		}
		return ids(cs)
	}

	if got := get(CandidateOpts{Scope: roundcfg.ScopeSlate, Slate: []string{"#14", "12", "11", "15"}}); !reflect.DeepEqual(got, []string{"12", "14"}) {
		t.Errorf("slate is the slate minus held and closed: %v", got)
	}
	if got := get(CandidateOpts{Scope: roundcfg.ScopeMilestone}); !reflect.DeepEqual(got, []string{"10"}) {
		t.Errorf("milestone is the active milestone's open, unheld items: %v", got)
	}
	if got := get(CandidateOpts{Scope: roundcfg.ScopeNext}); !reflect.DeepEqual(got, []string{"10"}) {
		t.Errorf("next stays in the active milestone while it has work: %v", got)
	}
	be.items["10"].Closed = true
	if got := get(CandidateOpts{Scope: roundcfg.ScopeMilestone}); len(got) != 0 {
		t.Errorf("milestone with nothing left is empty: %v", got)
	}
	// M02 depends on M01, which is active, not shipped, so it is not ready;
	// M03 depends on an unknown milestone. Ship M01 and M02 becomes next.
	if got := get(CandidateOpts{Scope: roundcfg.ScopeNext}); len(got) != 0 {
		t.Errorf("next must not roll into a milestone whose dependencies are unshipped: %v", got)
	}
	milestoneDoc(t, root, "M01", "shipped")
	if got := get(CandidateOpts{Scope: roundcfg.ScopeNext}); !reflect.DeepEqual(got, []string{"12"}) {
		t.Errorf("next rolls into the first ready planned milestone: %v", got)
	}
	if got := get(CandidateOpts{Scope: roundcfg.ScopeMilestone}); len(got) != 0 {
		t.Errorf("milestone never rolls over: %v", got)
	}
	if got := get(CandidateOpts{Scope: roundcfg.ScopeOpen}); !reflect.DeepEqual(got, []string{"12", "13", "14"}) {
		t.Errorf("open is every open item no slot holds: %v", got)
	}
	if _, err := e.Candidates(bg, root, be, CandidateOpts{Scope: "all"}); err == nil {
		t.Error("an unknown scope must fail")
	}
}

func TestCandidatesReadOverlapAgainstSlotChanges(t *testing.T) {
	root := newRepo(t, map[string]string{"ben": "ben/7-pool"})
	// ben has uncommitted work on a tracked file the issue text never names.
	if err := os.WriteFile(filepath.Join(root, "seed"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(root, ".worktrees", "ben")
	if err := os.WriteFile(filepath.Join(wt, "seed"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	milestoneDoc(t, root, "M01", "active")
	be := &fakeRemote{}
	be.add("7", "ben's issue", "M01", false, "")
	be.add("8", "wants the seed file", "M01", false, "edits seed")
	writeRegistry(t, root, slot(root, "ben", "ben/7-pool", nil))
	cs, err := (Env{Git: git.Exec, Base: "main"}).Candidates(bg, root, be, CandidateOpts{Scope: roundcfg.ScopeMilestone})
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || cs[0].ID != "8" || cs[0].Ready() || len(cs[0].Overlaps) != 1 || cs[0].Overlaps[0].Slot != "ben" {
		t.Fatalf("8 must collide with ben's real changes: %+v", cs)
	}
}

func fakeLease(host string, alive ...int) roundlease.Env {
	live := map[int]bool{}
	for _, p := range alive {
		live[p] = true
	}
	return roundlease.Env{
		Host:      host,
		Alive:     func(p int) bool { return live[p] },
		StartTime: func(p int) (uint64, bool) { return uint64(p) * 10, live[p] },
		Now:       func() time.Time { return time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC) },
	}
}

func startOpts(scope string, pid int, items ...string) StartOpts {
	set, _ := roundcfg.Load(os.TempDir()) // defaults: no config there
	// Dispatch tmux: these tests drive tab mode through a fake host.
	return StartOpts{Scope: scope, Items: items, HolderPID: pid, Settings: set, DefaultNum: 2, Dispatch: "tmux"}
}

func TestStartProvisionsRosterAndTakesLease(t *testing.T) {
	root := newRepo(t, nil)
	e := Env{Git: git.Exec, Base: "main", Getenv: noEnv, Lease: fakeLease("h", 100, 200)}
	st, err := e.Start(bg, root, startOpts(roundcfg.ScopeMilestone, 100))
	if err != nil {
		t.Fatal(err)
	}
	if st.Round != 1 || st.Outcome != roundlease.Taken || !st.Changed {
		t.Fatalf("%+v", st)
	}
	var names []string
	for _, s := range st.Slots {
		sl := worker.AsSlot(s)
		names = append(names, sl.Name())
		wt := filepath.Join(root, ".worktrees", sl.Name())
		if sl.Worktree() != wt || sl.Branch() != "park/"+sl.Name() {
			t.Errorf("slot %v must be .worktrees/<agent> on park/<agent>", s)
		}
		if _, err := os.Stat(wt); err != nil {
			t.Errorf("worktree missing: %v", err)
		}
	}
	if !reflect.DeepEqual(names, []string{"ben", "dana"}) {
		t.Fatalf("first two roster names: %v", names)
	}

	// The same holder again renews: same round, nothing re-provisioned.
	again, err := e.Start(bg, root, startOpts(roundcfg.ScopeMilestone, 100))
	if err != nil || again.Round != 1 || again.Outcome != roundlease.Renewed || again.Changed {
		t.Fatalf("restart must be idempotent: %v %+v", err, again)
	}
	// A second orchestrator, even from another worktree of the repo, is refused.
	wt := filepath.Join(root, ".worktrees", "ben")
	_, err = e.Start(bg, wt, startOpts(roundcfg.ScopeMilestone, 200))
	var we *exitcode.Error
	if !errors.As(err, &we) || we.Exit != exitcode.ExitRefused {
		t.Fatalf("second holder must be refused: %v", err)
	}
	if held, ok := we.Data.(*roundlease.HeldError); !ok || held.Lease.PID != 100 {
		t.Fatalf("refusal must name the holder: %+v", we.Data)
	}
	// Growing the pool keeps the round.
	o := startOpts(roundcfg.ScopeMilestone, 100)
	o.Slots = 3
	grown, err := e.Start(bg, root, o)
	if err != nil || len(grown.Slots) != 3 || grown.Round != 1 {
		t.Fatalf("grow: %v %+v", err, grown)
	}
}

func TestStartReclaimsStaleLeaseAndBumpsRound(t *testing.T) {
	root := newRepo(t, nil)
	first := Env{Git: git.Exec, Base: "main", Getenv: noEnv, Lease: fakeLease("h", 100)}
	if _, err := first.Start(bg, root, startOpts(roundcfg.ScopeMilestone, 100)); err != nil {
		t.Fatal(err)
	}
	if err := worker.Update(root, func(d *worker.Doc) { d.SetLayout("split"); d.SetCLIPane("pc") }); err != nil {
		t.Fatal(err)
	}
	second := Env{Git: git.Exec, Base: "main", Getenv: noEnv, Lease: fakeLease("h", 300)} // pid 100 is gone
	_, st, _ := second.ReadLease(bg, root)
	if st != roundlease.Stale {
		t.Fatalf("holder gone must read stale: %v", st)
	}
	rep, err := second.Status(bg, root)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, f := range rep.Findings {
		if f.Kind == LeaseStale && f.Repair == "" && strings.Contains(f.Detail, "pid 100") {
			found = true
		}
	}
	if !found {
		t.Fatalf("status must report lease-stale without a repair: %+v", rep.Findings)
	}
	if out, _ := second.Reconcile(bg, root, true); len(out.Repaired) != 0 {
		t.Errorf("reconcile --apply must not clear the lease: %+v", out.Repaired)
	}

	got, err := second.Start(bg, root, startOpts(roundcfg.ScopeMilestone, 300))
	if err != nil || got.Outcome != roundlease.Reclaimed || got.Round != 2 || got.Reclaimed.PID != 100 || len(got.Warnings) == 0 {
		t.Fatalf("reclaim: %v %+v", err, got)
	}
	if l := worker.LoadRegistry(root).Layout(); l != "" {
		t.Errorf("a new round starts in tabs, layout = %q (#205)", l)
	}
	if p := worker.LoadRegistry(root).CLIPane(); p != "pc" {
		t.Errorf("round start must keep the pane rota orchestrate recorded, cliPane = %q (#223)", p)
	}
}

func TestClearStaleLeaseLeavesLiveOnes(t *testing.T) {
	root := newRepo(t, nil)
	e := Env{Git: git.Exec, Base: "main", Getenv: noEnv, Lease: fakeLease("h", 100)}
	if _, err := e.Start(bg, root, startOpts(roundcfg.ScopeMilestone, 100)); err != nil {
		t.Fatal(err)
	}
	if _, cleared, _ := e.ClearStaleLease(bg, root); cleared {
		t.Fatal("a live lease must stay")
	}
	dead := Env{Git: git.Exec, Base: "main", Getenv: noEnv, Lease: fakeLease("h")}
	l, cleared, err := dead.ClearStaleLease(bg, root)
	if err != nil || !cleared || l.PID != 100 {
		t.Fatalf("a stale lease is cleared: %v %v %+v", err, cleared, l)
	}
}

func TestStartRecordsSlateAndValidatesFlags(t *testing.T) {
	root := newRepo(t, nil)
	e := Env{Git: git.Exec, Base: "main", Getenv: noEnv, Lease: fakeLease("h", 100)}
	for name, o := range map[string]StartOpts{
		"slate without items":    startOpts(roundcfg.ScopeSlate, 100),
		"items without slate":    startOpts(roundcfg.ScopeMilestone, 100, "12"),
		"more slots than roster": func() StartOpts { o := startOpts(roundcfg.ScopeMilestone, 100); o.Slots = 9; return o }(),
	} {
		_, err := e.Start(bg, root, o)
		var we *exitcode.Error
		if !errors.As(err, &we) || we.Exit != exitcode.ExitUsage {
			t.Errorf("%s: want a usage error, got %v", name, err)
		}
	}
	if _, st, _ := e.ReadLease(bg, root); st != roundlease.None {
		t.Errorf("a refused start must not take the lease: %v", st)
	}
	st, err := e.Start(bg, root, startOpts(roundcfg.ScopeSlate, 100, "#12", "13", "12"))
	if err != nil || !reflect.DeepEqual(st.Slate, []string{"12", "13"}) {
		t.Fatalf("%v %+v", err, st)
	}
	scope, slate := SlateOf(root)
	if scope != "slate" || !reflect.DeepEqual(slate, []string{"12", "13"}) {
		t.Errorf("recorded %q %v", scope, slate)
	}
	if _, err := e.Start(bg, root, startOpts(roundcfg.ScopeMilestone, 100)); err != nil {
		t.Fatal(err)
	}
	if scope, slate := SlateOf(root); scope != "milestone" || slate != nil {
		t.Errorf("a milestone round drops the slate: %q %v", scope, slate)
	}
}

func TestStartRenewKeepsRecordedScope(t *testing.T) {
	root := newRepo(t, nil)
	e := Env{Git: git.Exec, Base: "main", Getenv: noEnv, Lease: fakeLease("h", 100)}
	// --items without --scope is a slate.
	st, err := e.Start(bg, root, startOpts("", 100, "12", "13"))
	if err != nil || st.Scope != roundcfg.ScopeSlate || !reflect.DeepEqual(st.Slate, []string{"12", "13"}) {
		t.Fatalf("items alone make a slate: %v %+v", err, st)
	}
	// A renewal without --scope keeps scope and slate.
	st, err = e.Start(bg, root, startOpts("", 100))
	if err != nil || st.Scope != roundcfg.ScopeSlate || !reflect.DeepEqual(st.Slate, []string{"12", "13"}) {
		t.Fatalf("renew must keep the slate: %v %+v", err, st)
	}
	if scope, slate := SlateOf(root); scope != "slate" || !reflect.DeepEqual(slate, []string{"12", "13"}) {
		t.Errorf("recorded %q %v", scope, slate)
	}
	// --scope slate --items replaces the slate while holding the lease.
	if st, err = e.Start(bg, root, startOpts(roundcfg.ScopeSlate, 100, "14")); err != nil || !reflect.DeepEqual(st.Slate, []string{"14"}) {
		t.Fatalf("new slate: %v %+v", err, st)
	}
	// An explicit --scope changes it.
	if st, err = e.Start(bg, root, startOpts(roundcfg.ScopeOpen, 100)); err != nil || st.Scope != roundcfg.ScopeOpen {
		t.Fatalf("explicit scope: %v %+v", err, st)
	}
	if scope, slate := SlateOf(root); scope != "open" || slate != nil {
		t.Errorf("recorded %q %v", scope, slate)
	}
	// A renewal now keeps open.
	if st, err = e.Start(bg, root, startOpts("", 100)); err != nil || st.Scope != roundcfg.ScopeOpen {
		t.Fatalf("renew keeps open: %v %+v", err, st)
	}
}

func TestStartFreshWithoutScopeUsesConfig(t *testing.T) {
	root := newRepo(t, nil)
	e := Env{Git: git.Exec, Base: "main", Getenv: noEnv, Lease: fakeLease("h", 100)}
	o := startOpts("", 100)
	o.Settings.Scope = roundcfg.ScopeNext
	st, err := e.Start(bg, root, o)
	if err != nil || st.Scope != roundcfg.ScopeNext {
		t.Fatalf("fresh round uses round.scope: %v %+v", err, st)
	}
	// round.scope slate without items is refused, before the lease is taken.
	root = newRepo(t, nil)
	o.Settings.Scope = roundcfg.ScopeSlate
	var we *exitcode.Error
	if _, err := e.Start(bg, root, o); !errors.As(err, &we) || we.Exit != exitcode.ExitUsage {
		t.Fatalf("want usage error: %v", err)
	}
}

func TestStartNumbersALeaseTakenUnnumberedByTheSameHolder(t *testing.T) {
	root := newRepo(t, nil)
	le := fakeLease("h", 100, 200)
	// The keepalive supervisor (pid 100) took the lease without a number.
	cd, err := (Env{Git: git.Exec}).commonDir(bg, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := le.Acquire(cd, root, le.Discover(100, func(string) string { return "" }), 0); err != nil {
		t.Fatal(err)
	}
	e := Env{Git: git.Exec, Base: "main", Getenv: noEnv, Lease: le}
	// Its child runs `round start` with ROTA_ROUND_HOLDER_PID, no --holder-pid.
	o := startOpts(roundcfg.ScopeMilestone, 0)
	e.Getenv = func(k string) string {
		if k == roundlease.HolderPIDEnv {
			return "100"
		}
		return ""
	}
	st, err := e.Start(bg, root, o)
	if err != nil || st.Outcome != roundlease.Numbered || st.Round != 1 || !st.Changed {
		t.Fatalf("first start under the supervisor must number the lease: %v %+v", err, st)
	}
	if l, _, _ := e.ReadLease(bg, root); l.Round != 1 {
		t.Fatalf("lease must carry the number: %+v", l)
	}
	if r, _ := worker.LoadRegistry(root).Round(); r != 1 {
		t.Fatalf("registry round: %v", r)
	}
	// A restart of the orchestrator renews: the number survives.
	again, err := e.Start(bg, root, o)
	if err != nil || again.Outcome != roundlease.Renewed || again.Round != 1 || again.Changed {
		t.Fatalf("renewal under the supervisor must keep the round: %v %+v", err, again)
	}
	// A hand-run start from another process is refused.
	_, err = e.Start(bg, root, startOpts(roundcfg.ScopeMilestone, 200))
	var we *exitcode.Error
	if !errors.As(err, &we) || we.Exit != exitcode.ExitRefused {
		t.Fatalf("a non-descendant must be refused: %v", err)
	}
}

// TestItemBodyRoundTrip pins the grammar the writer (itembody) and the
// readiness reader share: what rota item create writes, round reads back.
func TestItemBodyRoundTrip(t *testing.T) {
	body := []byte("## Goal\nx\n\n### Depends on is not a dependency here\n")
	body = itembody.AppendDependsOn(body, []string{"#57", "B07"})
	body = itembody.AppendFiles(body, []string{"internal/cli/item.go", "internal/new/*.go"})
	refs, bad := Dependencies(string(body))
	if !reflect.DeepEqual(refs, []string{"57", "B07"}) || bad != nil {
		t.Errorf("depends read back %v %v", refs, bad)
	}
	if !itembody.HasDependsOn(body) {
		t.Error("HasDependsOn misses the written heading")
	}
	got := Footprint(string(body), tracked, nil)
	if !reflect.DeepEqual(got, []string{"internal/cli/item.go", "internal/new/*.go"}) {
		t.Errorf("files read back %v", got)
	}
}
