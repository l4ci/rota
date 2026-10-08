package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/reap"
	"github.com/l4ci/rota/internal/round"
)

type reapTestHost struct{ tabs []host.Tab }

func (h reapTestHost) Tabs(context.Context) ([]host.Tab, error)        { return h.tabs, nil }
func (reapTestHost) Processes(context.Context) ([]reap.Process, error) { return nil, nil }
func (reapTestHost) CloseTab(_ context.Context, id string) error {
	return fmt.Errorf("close %s: refused", id)
}
func (reapTestHost) StopProcess(context.Context, int) error { return nil }

// reapProject: base feat/x; worktrees old (clean, merged), dirty (untracked
// file), live (an agent works in it); branch kit/9-gone merged and unowned.
func reapProject(t *testing.T, ops reap.HostOps, hostUp bool) (string, *Deps) {
	t.Helper()
	root := gitRepo(t)
	for name, br := range map[string]string{"old": "kit/1-old", "dirty": "kit/2-dirty", "live": "kit/3-live"} {
		gitIn(t, root, "worktree", "add", "-q", "-b", br, filepath.Join(root, ".worktrees", name), "HEAD")
	}
	gitIn(t, root, "branch", "kit/9-gone", "HEAD")
	if err := os.WriteFile(filepath.Join(root, ".worktrees", "dirty", "wip.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := testDeps()
	d.ReapEnv = func(context.Context, string) (round.Env, reap.HostOps) {
		e := round.Env{Git: git.Exec, Base: "feat/x", HostName: "herdr"}
		if hostUp {
			e.Snapshot = func(context.Context) ([]host.Agent, error) {
				return []host.Agent{{Tab: "w3:t1", Name: "live", Cwd: filepath.Join(root, ".worktrees", "live"), Status: "working"}}, nil
			}
		} else {
			e.HostErr = "fake: host down"
		}
		return e, ops
	}
	return root, d
}

func candIDs(d map[string]any) []string {
	var out []string
	for _, c := range d["candidates"].([]any) {
		m := c.(map[string]any)
		s := m["id"].(string)
		if m["held"] != nil {
			s += " held"
		}
		out = append(out, s)
	}
	return out
}

func TestReapPreviewThenApply(t *testing.T) {
	root, deps := reapProject(t, nil, true)
	code, out, errOut := rotaInWith(t, deps, root, "--json", "reap")
	d := data(t, out)
	if code != 0 || d["changed"] != false || len(d["reaped"].([]any)) != 0 || len(d["failed"].([]any)) != 0 {
		t.Fatalf("preview = %d %v", code, d)
	}
	want := []string{"worktree:dirty held", "worktree:old", "branch:kit/9-gone"}
	if got := candIDs(d); !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates = %v", got)
	}
	if !strings.Contains(out, "preview only; pass --apply") && !strings.Contains(errOut, "preview only; pass --apply") {
		t.Errorf("no preview warning: %s %s", out, errOut)
	}
	if _, err := os.Stat(filepath.Join(root, ".worktrees", "old")); err != nil {
		t.Fatal("preview deleted a worktree")
	}

	code, out, _ = rotaInWith(t, deps, root, "--json", "reap", "--apply")
	d = data(t, out)
	if code != 0 || d["changed"] != true || !reflect.DeepEqual(d["reaped"], []any{"worktree:old", "branch:kit/9-gone"}) {
		t.Fatalf("apply = %d %v", code, d)
	}
	for name, gone := range map[string]bool{"old": true, "dirty": false, "live": false} {
		_, err := os.Stat(filepath.Join(root, ".worktrees", name))
		if gone != (err != nil) {
			t.Errorf("worktree %s: gone=%v", name, err != nil)
		}
	}
	if _, out, _ = rotaInWith(t, deps, root, "--json", "reap"); !reflect.DeepEqual(candIDs(data(t, out)), []string{"worktree:dirty held", "branch:kit/1-old"}) { // old's branch is unowned only now
		t.Errorf("after apply: %s", out)
	}
}

func TestReapKindFilterAndUnknownKind(t *testing.T) {
	root, deps := reapProject(t, nil, true)
	_, out, _ := rotaInWith(t, deps, root, "--json", "reap", "--kind", "branch")
	if got := candIDs(data(t, out)); !reflect.DeepEqual(got, []string{"branch:kit/9-gone"}) {
		t.Errorf("--kind branch = %v", got)
	}
	if code, _, _ := rotaInWith(t, deps, root, "--json", "reap", "--kind", "bogus"); code != 2 {
		t.Errorf("unknown kind exit %d, want 2", code)
	}
	if code, _, _ := rotaInWith(t, deps, root, "--json", "reap", "--kind", "branch,nope"); code != 2 {
		t.Errorf("partly unknown kind exit %d, want 2", code)
	}
	if code, _, _ := rotaInWith(t, deps, root, "--json", "reap", "--repo", "x"); code != 2 {
		t.Errorf("--repo exit %d, want 2", code)
	}
}

func TestReapHostDownListsNoWorktrees(t *testing.T) {
	root, deps := reapProject(t, nil, false)
	code, out, errOut := rotaInWith(t, deps, root, "--json", "reap", "--apply")
	d := data(t, out)
	if code != 0 || !reflect.DeepEqual(candIDs(d), []string{"branch:kit/9-gone"}) {
		t.Fatalf("host down = %d %v", code, d)
	}
	if !strings.Contains(out+errOut, "host unavailable") {
		t.Errorf("no host warning: %s %s", out, errOut)
	}
	if _, err := os.Stat(filepath.Join(root, ".worktrees", "old")); err != nil {
		t.Error("a worktree was removed with no host data")
	}
}

func TestReapFailureExitsOneWithData(t *testing.T) {
	ops := reapTestHost{tabs: []host.Tab{{ID: "w9:t1", Cwds: []string{"/nowhere"}, Agentless: true}}}
	root, deps := reapProject(t, ops, true)
	ops.tabs[0].Cwds = []string{filepath.Join(root, ".worktrees", "old")} // a dead tab in the free checkout
	code, out, _ := rotaInWith(t, deps, root, "--json", "reap", "--apply")
	d := data(t, out)
	failed := d["failed"].([]any)
	if code != 1 || len(failed) != 1 || failed[0].(map[string]any)["id"] != "tab:w9:t1" {
		t.Fatalf("apply = %d %v", code, d)
	}
	if !reflect.DeepEqual(d["reaped"], []any{"worktree:old", "branch:kit/9-gone"}) || d["changed"] != true {
		t.Errorf("the rest must still be removed: %v", d)
	}
}

// A host's tabs and processes are reaped through the ops the env hands reap; a
// live agent's tab and process stay. (Replaces the ROTA_TEST_REAP_HOST smoke.)
func TestReapApplyClosesDeadTabAndProcess(t *testing.T) {
	ops := &recordingReapOps{
		tabs:  []host.Tab{{ID: "w5:t1", Agentless: true}, {ID: "w3:t1", Agentless: false}},
		procs: []reap.Process{{PID: 4242, Name: "npm", Tab: "w5:t1"}, {PID: 4343, Name: "node", Tab: "w3:t1"}},
	}
	root, deps := reapProject(t, ops, true)
	ops.tabs[0].Cwds = []string{filepath.Join(root, ".worktrees", "old")}
	ops.tabs[1].Cwds = []string{filepath.Join(root, ".worktrees", "live")}
	ops.procs[0].Cwd = filepath.Join(root, ".worktrees", "old")
	ops.procs[1].Cwd = filepath.Join(root, ".worktrees", "live")
	_, out, _ := rotaInWith(t, deps, root, "--json", "reap", "--apply")
	d := data(t, out)
	if !reflect.DeepEqual(d["reaped"], []any{"worktree:old", "branch:kit/9-gone", "tab:w5:t1", "process:4242"}) {
		t.Fatalf("reaped = %v", d["reaped"])
	}
	if !reflect.DeepEqual(ops.removed, []string{"tab w5:t1", "process 4242"}) {
		t.Errorf("host removals = %v", ops.removed)
	}
}

type recordingReapOps struct {
	tabs    []host.Tab
	procs   []reap.Process
	removed []string
}

func (o *recordingReapOps) Tabs(context.Context) ([]host.Tab, error)          { return o.tabs, nil }
func (o *recordingReapOps) Processes(context.Context) ([]reap.Process, error) { return o.procs, nil }
func (o *recordingReapOps) CloseTab(_ context.Context, id string) error {
	o.removed = append(o.removed, "tab "+id)
	return nil
}
func (o *recordingReapOps) StopProcess(_ context.Context, pid int) error {
	o.removed = append(o.removed, fmt.Sprintf("process %d", pid))
	return nil
}

// One swap of Deps.Host reaches every hook that builds a host.
func TestDepsHostFactoryFeedsEveryHook(t *testing.T) {
	d := defaultDeps()
	var asked []string
	fake := &cliHost{}
	d.Host = func(kind string) host.Host { asked = append(asked, kind); return fake }
	if d.workerEnv().NewHost("tmux") != host.Host(fake) ||
		d.escalationEnv().Host() != host.Host(fake) ||
		d.orchestrateEnv().Host("herdr") != host.Host(fake) ||
		d.RoundEnv(context.Background(), t.TempDir()).Worker.NewHost("tmux") != host.Host(fake) {
		t.Fatal("a hook built its own host instead of asking Deps.Host")
	}
	if want := []string{"tmux", "herdr", "herdr", "tmux"}; !reflect.DeepEqual(asked[:4], want) {
		t.Errorf("asked = %v, want prefix %v", asked, want)
	}
}

func TestDefaultReapEnvBuildsNoForge(t *testing.T) {
	root := trackerProject(t, "")
	gitIn(t, root, "init", "-q")
	d := testDeps()
	builds := forgeBuilds(d)
	e, _ := d.ReapEnv(context.Background(), root)
	if *builds != 0 || e.Forge != nil {
		t.Errorf("reap env built %d forge(s), Forge %v", *builds, e.Forge)
	}
}
