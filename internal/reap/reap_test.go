package reap

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundlease"
)

var bg = context.Background()

const root = "/r"

// fakeGit answers by "dir|args" then "args"; an unscripted call exits 1.
type fakeGit struct {
	resp  map[string]gitResp
	calls []string
}

type gitResp struct {
	out, err string
	code     int
}

func (f *fakeGit) run(_ context.Context, dir string, args ...string) (git.Result, error) {
	a := strings.Join(args, " ")
	f.calls = append(f.calls, dir+"|"+a)
	if r, ok := f.resp[dir+"|"+a]; ok {
		return git.Result{Stdout: r.out, Stderr: r.err, ExitCode: r.code}, nil
	}
	if r, ok := f.resp[a]; ok {
		return git.Result{Stdout: r.out, Stderr: r.err, ExitCode: r.code}, nil
	}
	return git.Result{ExitCode: 1}, nil
}

func (f *fakeGit) ran(prefix string) bool {
	for _, c := range f.calls {
		if strings.Contains(c, prefix) {
			return true
		}
	}
	return false
}

type wt struct{ name, branch, extra string }

// repo scripts a project with main checked out at /r and the given
// worktrees under /r/.worktrees, and branches as the local branch list.
// merged names the refs that are ancestors of main.
func repo(wts []wt, branches []string, merged ...string) *fakeGit {
	list := "worktree /r\nHEAD abc\nbranch refs/heads/main\n\n"
	for _, w := range wts {
		list += "worktree /r/.worktrees/" + w.name + "\nHEAD abc\n"
		if w.branch != "" {
			list += "branch refs/heads/" + w.branch + "\n"
		}
		if w.extra != "" {
			list += w.extra + "\n"
		}
		list += "\n"
	}
	f := &fakeGit{resp: map[string]gitResp{
		"worktree list --porcelain":                          {out: list},
		"for-each-ref --format=%(refname:short) refs/heads/": {out: strings.Join(branches, "\n")},
	}}
	for _, m := range merged {
		f.resp["merge-base --is-ancestor "+m+" main"] = gitResp{}
	}
	return f
}

func (f *fakeGit) clean(name string) {
	f.resp["/r/.worktrees/"+name+"|status --porcelain"] = gitResp{}
}

type fakeHost struct {
	tabs      []host.Tab
	procs     []Process
	tabsErr   error
	closed    []string
	stopped   []int
	closeErr  map[string]error
	afterTabs func() // runs after each Tabs call
}

func (h *fakeHost) Tabs(context.Context) ([]host.Tab, error) {
	t := h.tabs
	if h.afterTabs != nil {
		defer h.afterTabs()
	}
	return t, h.tabsErr
}
func (h *fakeHost) Processes(context.Context) ([]Process, error) { return h.procs, nil }
func (h *fakeHost) CloseTab(_ context.Context, id string) error {
	if err := h.closeErr[id]; err != nil {
		return err
	}
	h.closed = append(h.closed, id)
	return nil
}
func (h *fakeHost) StopProcess(_ context.Context, pid int) error {
	h.stopped = append(h.stopped, pid)
	return nil
}

func input(g *fakeGit, rows []round.Row, agents []host.Agent, h HostOps) Input {
	return Input{Root: root, Base: "main", Git: g.run, Report: &round.Report{Rows: rows}, Agents: agents, Host: h}
}

func ids(cs []Candidate) []string {
	out := []string{}
	for _, c := range cs {
		s := c.ID
		if c.Held != "" {
			s += " HELD(" + c.Held + ")"
		}
		out = append(out, s)
	}
	return out
}

func find(t *testing.T, in Input, kinds ...string) Result {
	t.Helper()
	res, err := Find(bg, in, kinds)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestUnregisteredWorkerWithLiveAgentIsNeverACandidate(t *testing.T) {
	g := repo([]wt{{"dana", "dana/58-x", ""}}, []string{"dana/58-x"}, "dana/58-x", "HEAD")
	g.clean("dana")
	// A herdr agent nobody registered, working in a subdirectory of its checkout.
	agents := []host.Agent{{Tab: "w1:t1", Name: "dana", Cwd: "/r/.worktrees/dana/internal", Status: "working"}}
	res := find(t, input(g, nil, agents, nil))
	if len(res.Candidates) != 0 {
		t.Errorf("a live unregistered worker was listed: %v", ids(res.Candidates))
	}
	// Same when only the round row says an agent is matched.
	rows := []round.Row{{Name: "dana", Agent: "dana", Branch: "dana/58-x"}}
	if res := find(t, input(g, rows, nil, nil)); len(res.Candidates) != 0 {
		t.Errorf("row with a matched agent was listed: %v", ids(res.Candidates))
	}
}

func TestSameCheckoutWithoutAgentIsListed(t *testing.T) {
	g := repo([]wt{{"dana", "dana/58-x", ""}}, []string{"dana/58-x"}, "dana/58-x", "HEAD")
	g.clean("dana")
	res := find(t, input(g, nil, []host.Agent{}, nil))
	if got := ids(res.Candidates); !reflect.DeepEqual(got, []string{"worktree:dana"}) {
		t.Errorf("candidates = %v", got) // the branch is checked out, so it is not one
	}
}

func TestParkAndBaseAndRegisteredAndLockedWorktreesAreNeverCandidates(t *testing.T) {
	g := repo([]wt{
		{"ben", "park/ben", ""},
		{"onmain", "main", ""},
		{"slot", "kit/3-a", ""},
		{"locked", "kit/4-a", "locked in use"},
	}, []string{"park/ben", "kit/3-a", "kit/4-a"}, "park/ben", "kit/3-a", "kit/4-a", "HEAD")
	for _, n := range []string{"ben", "onmain", "slot", "locked"} {
		g.clean(n)
	}
	rows := []round.Row{{Name: "slot", Registered: true, Branch: "kit/3-a"}}
	res := find(t, input(g, rows, []host.Agent{}, nil))
	if len(res.Candidates) != 0 {
		t.Errorf("protected entries listed: %v", ids(res.Candidates))
	}
}

func TestBranchesCheckedOutOrParkedOrRegisteredAreSkipped(t *testing.T) {
	g := repo([]wt{{"dana", "dana/58-x", ""}}, []string{"dana/58-x", "park/ben", "kit/9-old", "kit/10-slot", "kit/11-wip", "main", "notes"},
		"dana/58-x", "park/ben", "kit/9-old", "kit/10-slot")
	g.resp["rev-list --count main..kit/11-wip"] = gitResp{out: "3\n"}
	rows := []round.Row{{Name: "kit", Registered: true, Branch: "kit/10-slot"}}
	res := find(t, input(g, rows, []host.Agent{}, nil), KindBranch)
	want := []string{"branch:kit/11-wip HELD(3 commit(s) not on main)", "branch:kit/9-old"}
	if got := ids(res.Candidates); !reflect.DeepEqual(got, want) {
		t.Errorf("candidates = %v", got)
	}
}

func TestHeldWorktreeIsListedAndNeverDeleted(t *testing.T) {
	g := repo([]wt{{"dirty", "kit/1-a", ""}, {"ahead", "kit/2-a", ""}, {"clean", "kit/3-a", ""}}, nil, "kit/3-a")
	g.resp["/r/.worktrees/dirty|status --porcelain"] = gitResp{out: " M file.go\n"}
	g.clean("ahead")
	g.clean("clean")
	g.resp["/r/.worktrees/clean|merge-base --is-ancestor HEAD main"] = gitResp{}
	g.resp["/r/.worktrees/ahead|rev-list --count main..HEAD"] = gitResp{out: "2\n"}
	in := input(g, nil, []host.Agent{}, nil)
	res := find(t, in, KindWorktree)
	want := []string{"worktree:ahead HELD(2 commit(s) not on main)", "worktree:clean", "worktree:dirty HELD(uncommitted changes)"}
	if got := ids(res.Candidates); !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates = %v", got)
	}
	g.calls = nil
	g.resp["worktree remove /r/.worktrees/clean"] = gitResp{}
	out := Apply(bg, in, res.Candidates)
	if !reflect.DeepEqual(out.Reaped, []string{"worktree:clean"}) || len(out.Failed) != 0 {
		t.Errorf("apply = %+v", out)
	}
	if g.ran("dirty") && g.ran("worktree remove /r/.worktrees/dirty") || g.ran("worktree remove /r/.worktrees/ahead") {
		t.Errorf("a held worktree was removed: %v", g.calls)
	}
	if g.ran("--force") || g.ran("-f ") {
		t.Errorf("worktree removal must not force: %v", g.calls)
	}
}

func TestStatusFailureHoldsTheWorktree(t *testing.T) {
	g := repo([]wt{{"odd", "kit/1-a", ""}}, nil)
	g.resp["/r/.worktrees/odd|status --porcelain"] = gitResp{code: 128, err: "fatal"}
	res := find(t, input(g, nil, []host.Agent{}, nil), KindWorktree)
	if got := ids(res.Candidates); !reflect.DeepEqual(got, []string{"worktree:odd HELD(git status failed)"}) {
		t.Errorf("candidates = %v", got)
	}
}

func TestPrunableWorktreeIsACandidate(t *testing.T) {
	g := repo([]wt{{"gone", "kit/1-a", "prunable gitdir file points to non-existent location"}}, nil)
	res := find(t, input(g, nil, []host.Agent{}, nil), KindWorktree)
	if len(res.Candidates) != 1 || res.Candidates[0].Held != "" || !strings.Contains(res.Candidates[0].Reason, "prunable") {
		t.Errorf("candidates = %+v", res.Candidates)
	}
}

func tab(id, cwd string, agentless bool) host.Tab {
	return host.Tab{ID: id, Cwds: []string{cwd}, Agentless: agentless}
}

func TestTabsOnlyWhenNoAgentIsProvenAndOwnerIsFree(t *testing.T) {
	g := repo([]wt{{"done", "kit/1-a", ""}, {"ben", "park/ben", ""}, {"live", "kit/2-a", ""}, {"slot", "kit/3-a", ""}}, nil)
	h := &fakeHost{tabs: []host.Tab{
		tab("w1:t1", "/r/.worktrees/done", true),  // dead agent, free checkout: listed
		tab("w2:t1", "/r/.worktrees/gone", true),  // checkout already removed: listed
		tab("w3:t1", "/r/.worktrees/live", true),  // another agent works in this checkout
		tab("w4:t1", "/r/.worktrees/done", false), // host cannot prove it agentless
		tab("w5:t1", "/r/.worktrees/ben", true),   // parked worker's tab
		tab("w6:t1", "/r/.worktrees/slot", true),  // registered slot
		tab("w7:t1", "/elsewhere/proj", true),     // another project
		tab("w8:t1", "/r", true),                  // the main checkout
		tab("w9:t1", "/r/.worktrees/gone2", true), // snapshot says an agent has it
	}}
	agents := []host.Agent{
		{Tab: "w3:t9", Name: "x", Cwd: "/r/.worktrees/live", Status: "working"},
		{Tab: "w9:t1", Name: "y", Cwd: "/elsewhere/y", Status: "working"},
	}
	rows := []round.Row{{Name: "slot", Registered: true, Branch: "kit/3-a", Tab: "w6:t1"}}
	res := find(t, input(g, rows, agents, h), KindTab)
	if got := ids(res.Candidates); !reflect.DeepEqual(got, []string{"tab:w1:t1", "tab:w2:t1"}) {
		t.Errorf("tabs = %v", got)
	}
}

func TestProcessUnderLiveAgentTabIsNotListed(t *testing.T) {
	g := repo(nil, nil)
	h := &fakeHost{procs: []Process{
		{PID: 10, Name: "npm", Tab: "w1:t1", Cwd: "/r/.worktrees/old"},
		{PID: 11, Name: "node", Tab: "w2:t1", Cwd: "/r/.worktrees/old"}, // agent on this tab
		{PID: 12, Name: "sleep", Tab: "w3:t1", Cwd: "/elsewhere"},
	}}
	agents := []host.Agent{{Tab: "w2:t1", Name: "a", Cwd: "/elsewhere/a", Status: "working"}}
	res := find(t, input(g, nil, agents, h), KindProcess)
	if got := ids(res.Candidates); !reflect.DeepEqual(got, []string{"process:10"}) {
		t.Errorf("processes = %v", got)
	}
}

func TestHostUnavailableFailsClosed(t *testing.T) {
	g := repo([]wt{{"done", "kit/1-a", ""}}, []string{"kit/9-old"}, "kit/9-old", "HEAD")
	g.clean("done")
	h := &fakeHost{tabs: []host.Tab{tab("w1:t1", "/r/.worktrees/done", true)}, procs: []Process{{PID: 5, Tab: "w1:t1", Cwd: "/r/.worktrees/done"}}}
	in := input(g, nil, nil, h)
	in.Report.Unavailable = []string{round.SourceHost}
	res := find(t, in)
	if got := ids(res.Candidates); !reflect.DeepEqual(got, []string{"branch:kit/9-old"}) {
		t.Errorf("with no host only git-provable branches may be listed, got %v", got)
	}
	if len(res.Warnings) == 0 || !strings.Contains(strings.Join(res.Warnings, "|"), "host unavailable") {
		t.Errorf("warnings = %v", res.Warnings)
	}
}

func TestTabsErrorDropsTabsWithWarning(t *testing.T) {
	g := repo(nil, nil)
	h := &fakeHost{tabsErr: fmt.Errorf("boom")}
	res := find(t, input(g, nil, []host.Agent{}, h), KindTab)
	if len(res.Candidates) != 0 || len(res.Warnings) != 1 {
		t.Errorf("%+v", res)
	}
}

func TestApplyRemovesOnlyUnheldAndReportsPartialFailure(t *testing.T) {
	g := repo([]wt{{"a", "kit/1-a", ""}, {"b", "kit/2-a", ""}}, []string{"kit/5-ok", "kit/6-wip"}, "kit/5-ok", "HEAD")
	g.clean("a")
	g.clean("b")
	g.resp["worktree remove /r/.worktrees/a"] = gitResp{code: 1, err: "fatal: contains modified files"}
	g.resp["rev-list --count main..kit/6-wip"] = gitResp{out: "1"}
	g.resp["worktree remove /r/.worktrees/b"] = gitResp{}
	g.resp["branch -D kit/5-ok"] = gitResp{}
	h := &fakeHost{tabs: []host.Tab{tab("w1:t1", "/r/.worktrees/old", true)}}
	in := input(g, nil, []host.Agent{}, h)
	res := find(t, in)
	wantIDs := []string{"worktree:a", "worktree:b", "branch:kit/5-ok", "branch:kit/6-wip HELD(1 commit(s) not on main)", "tab:w1:t1"}
	if got := ids(res.Candidates); !reflect.DeepEqual(got, wantIDs) {
		t.Fatalf("candidates = %v", got)
	}
	for _, c := range []string{"worktree remove", "branch -D"} {
		if g.ran(c) {
			t.Fatalf("Find ran %q", c)
		}
	}
	out := Apply(bg, in, res.Candidates)
	if !reflect.DeepEqual(out.Reaped, []string{"worktree:b", "branch:kit/5-ok", "tab:w1:t1"}) {
		t.Errorf("reaped = %v", out.Reaped)
	}
	if len(out.Failed) != 1 || out.Failed[0].ID != "worktree:a" || !strings.Contains(out.Failed[0].Error, "modified files") {
		t.Errorf("failed = %+v", out.Failed)
	}
	if g.ran("kit/6-wip|") || g.ran("branch -D kit/6-wip") {
		t.Errorf("held branch deleted: %v", g.calls)
	}
	if !reflect.DeepEqual(h.closed, []string{"w1:t1"}) {
		t.Errorf("closed = %v", h.closed)
	}
}

func TestApplyNeverClosesATabAnAgentTookOver(t *testing.T) {
	g := repo(nil, nil)
	h := &fakeHost{tabs: []host.Tab{tab("w1:t1", "/r/.worktrees/old", true)}}
	in := input(g, nil, []host.Agent{}, h)
	res := find(t, in, KindTab)
	h.tabs = []host.Tab{tab("w1:t1", "/r/.worktrees/old", false)} // an agent started in it since
	out := Apply(bg, in, res.Candidates)
	if len(h.closed) != 0 || len(out.Failed) != 1 {
		t.Errorf("closed %v, failed %+v", h.closed, out.Failed)
	}
}

func TestApplyReprovesABranchBeforeDeleting(t *testing.T) {
	g := repo(nil, []string{"kit/5-ok"}, "kit/5-ok")
	in := input(g, nil, []host.Agent{}, nil)
	res := find(t, in, KindBranch)
	g.resp["branch -D kit/5-ok"] = gitResp{}
	delete(g.resp, "merge-base --is-ancestor kit/5-ok main") // no longer merged
	out := Apply(bg, in, res.Candidates)
	if len(out.Reaped) != 0 || len(out.Failed) != 1 || g.ran("branch -D") {
		t.Errorf("apply = %+v, calls %v", out, g.calls)
	}
}

func TestKindFilter(t *testing.T) {
	g := repo([]wt{{"done", "kit/1-a", ""}}, []string{"kit/9-old"}, "kit/9-old", "HEAD")
	g.clean("done")
	all := find(t, input(g, nil, []host.Agent{}, nil))
	if len(all.Candidates) != 2 {
		t.Fatalf("all = %v", ids(all.Candidates))
	}
	for _, c := range all.Candidates {
		known := false
		for _, k := range Kinds {
			known = known || c.Kind == k
		}
		if !known {
			t.Errorf("unknown kind %q", c.Kind)
		}
	}
	only := find(t, input(g, nil, []host.Agent{}, nil), KindBranch)
	if got := ids(only.Candidates); !reflect.DeepEqual(got, []string{"branch:kit/9-old"}) {
		t.Errorf("filtered = %v", got)
	}
}

func TestGitFailureIsAnError(t *testing.T) {
	g := &fakeGit{resp: map[string]gitResp{"worktree list --porcelain": {code: 128, err: "not a repo"}}}
	if _, err := Find(bg, input(g, nil, nil, nil), nil); err == nil {
		t.Error("git failing must be an error")
	}
}

// fakeLease scripts the round lease.
type fakeLease struct {
	l        round.Lease
	st       roundlease.State
	readErr  error
	clearErr error
	stale    bool // ClearStaleLease finds it stale and clears it
	cleared  int
}

func (f *fakeLease) ReadLease(context.Context, string) (round.Lease, round.LeaseState, error) {
	return f.l, f.st, f.readErr
}

func (f *fakeLease) ClearStaleLease(context.Context, string) (round.Lease, bool, error) {
	if f.clearErr != nil || !f.stale {
		return round.Lease{}, false, f.clearErr
	}
	f.cleared++
	return f.l, true, nil
}

func leaseInput(f *fakeLease) Input {
	in := input(repo(nil, nil), nil, []host.Agent{}, nil)
	in.Lease = f
	return in
}

var deadHolder = round.Lease{PID: 4242, Host: "h", Round: 3, StartedAt: "2026-01-02T03:04:05Z"}

func TestStaleLeaseIsListedAndClearedOnApply(t *testing.T) {
	f := &fakeLease{l: deadHolder, st: roundlease.Stale, stale: true}
	in := leaseInput(f)
	res := find(t, in)
	if got := ids(res.Candidates); !reflect.DeepEqual(got, []string{"lease:round"}) {
		t.Fatalf("candidates = %v", got)
	}
	c := res.Candidates[0]
	if c.Held != "" || !strings.Contains(c.Reason, "pid 4242") || !strings.Contains(c.Reason, "round 3") {
		t.Errorf("candidate = %+v", c)
	}
	if f.cleared != 0 {
		t.Error("find must not clear the lease")
	}
	out := Apply(bg, in, res.Candidates)
	if !reflect.DeepEqual(out.Reaped, []string{"lease:round"}) || len(out.Failed) != 0 || f.cleared != 1 {
		t.Errorf("apply = %+v, cleared %d", out, f.cleared)
	}
}

func TestUnreadableStaleLeaseNamesNoHolder(t *testing.T) {
	f := &fakeLease{st: roundlease.Stale}
	res := find(t, leaseInput(f))
	if len(res.Candidates) != 1 || !strings.Contains(res.Candidates[0].Reason, "unreadable") {
		t.Errorf("candidates = %+v", res.Candidates)
	}
}

func TestLiveForeignAndAbsentLeasesAreNotListed(t *testing.T) {
	for _, st := range []roundlease.State{roundlease.Live, roundlease.Foreign, roundlease.None} {
		f := &fakeLease{l: deadHolder, st: st, stale: true}
		res := find(t, leaseInput(f))
		if len(res.Candidates) != 0 {
			t.Errorf("%s lease listed: %v", st, ids(res.Candidates))
		}
	}
	if res := find(t, input(repo(nil, nil), nil, []host.Agent{}, nil)); len(res.Candidates) != 0 {
		t.Errorf("nil lease access listed %v", ids(res.Candidates))
	}
}

func TestLeaseReadErrorIsAWarning(t *testing.T) {
	f := &fakeLease{st: roundlease.Stale, readErr: fmt.Errorf("boom")}
	res := find(t, leaseInput(f))
	if len(res.Candidates) != 0 || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "boom") {
		t.Errorf("res = %+v", res)
	}
}

func TestApplyLeaseThatTurnedLiveIsLeftAlone(t *testing.T) {
	f := &fakeLease{l: deadHolder, st: roundlease.Stale, stale: false}
	in := leaseInput(f)
	res := find(t, in)
	out := Apply(bg, in, res.Candidates)
	if len(out.Reaped) != 0 || len(out.Failed) != 1 || out.Failed[0].ID != "lease:round" {
		t.Errorf("apply = %+v", out)
	}
	f.clearErr = fmt.Errorf("locked")
	if out := Apply(bg, in, res.Candidates); len(out.Failed) != 1 || !strings.Contains(out.Failed[0].Error, "locked") {
		t.Errorf("apply = %+v", out)
	}
}

func TestLeaseKindFilter(t *testing.T) {
	g := repo([]wt{{"done", "kit/1-a", ""}}, []string{"kit/9-old"}, "kit/9-old", "HEAD")
	g.clean("done")
	in := input(g, nil, []host.Agent{}, nil)
	in.Lease = &fakeLease{l: deadHolder, st: roundlease.Stale}
	if got := ids(find(t, in, KindLease).Candidates); !reflect.DeepEqual(got, []string{"lease:round"}) {
		t.Errorf("lease only = %v", got)
	}
	if got := ids(find(t, in, KindBranch).Candidates); !reflect.DeepEqual(got, []string{"branch:kit/9-old"}) {
		t.Errorf("branch only = %v", got)
	}
}

// An adopted slot is released with its worktree kept, and a tool like Codex
// keeps that worktree outside .worktrees/. Once its PR merged the branch is
// listed for the user to delete, held while the checkout remains.
func TestReapListsMergedExternal(t *testing.T) {
	g := repo(nil, []string{"codex/12-thing", "codex/13-wip"}, "codex/12-thing")
	g.resp["rev-list --count main..codex/13-wip"] = gitResp{out: "2\n"}
	g.resp["worktree list --porcelain"] = gitResp{out: g.resp["worktree list --porcelain"].out +
		"worktree /elsewhere/a\nHEAD abc\nbranch refs/heads/codex/12-thing\n\n" +
		"worktree /elsewhere/b\nHEAD abc\nbranch refs/heads/codex/13-wip\n\n"}
	in := input(g, nil, []host.Agent{}, nil)
	res := find(t, in, KindBranch)
	want := []string{"branch:codex/12-thing HELD(checked out at /elsewhere/a; remove that worktree first)"}
	if got := ids(res.Candidates); !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
	if res.Candidates[0].Reason != "merged into main, held by a worktree outside .worktrees/" {
		t.Errorf("reason = %q", res.Candidates[0].Reason)
	}
	out := Apply(bg, in, res.Candidates)
	if len(out.Reaped) != 0 || g.ran("branch -D") || g.ran("worktree remove") {
		t.Errorf("a held external branch was deleted: %v %v", out.Reaped, g.calls)
	}
}

// An adopted branch need not lead with an issue number: codex/issue-612 is
// listed like codex/612-x.
func TestReapListsMergedExternalWithoutALeadingNumber(t *testing.T) {
	g := repo(nil, []string{"codex/issue-612"}, "codex/issue-612")
	g.resp["worktree list --porcelain"] = gitResp{out: g.resp["worktree list --porcelain"].out +
		"worktree /elsewhere/a\nHEAD abc\nbranch refs/heads/codex/issue-612\n\n"}
	res := find(t, input(g, nil, []host.Agent{}, nil), KindBranch)
	want := []string{"branch:codex/issue-612 HELD(checked out at /elsewhere/a; remove that worktree first)"}
	if got := ids(res.Candidates); !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
}
