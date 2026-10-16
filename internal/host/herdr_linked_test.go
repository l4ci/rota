package host

import (
	"reflect"
	"strings"
	"testing"
)

// linkedFake plays herdr for a LinkWorkspace dispatch: the orchestrator is in
// w9, `worktree open` makes w9N with root tab w9N:t1, `tab create` makes w9N:t2.
func linkedFake(alreadyOpen string, fail map[string]Result) *fake {
	return &fake{handler: func(_ string, a []string) Result {
		key := a[0] + " " + a[1]
		if r, ok := fail[key]; ok {
			return r
		}
		switch key {
		case "worktree open":
			return Result{Stdout: `{"id":"cli","result":{"already_open":` + alreadyOpen + `,"workspace":{"workspace_id":"w9N"},"tab":{"tab_id":"w9N:t1"},"root_pane":{"pane_id":"w9N:p1"}}}`}
		case "tab create":
			return Result{Stdout: `{"id":"cli","result":{"tab":{"tab_id":"w9N:t2"},"root_pane":{"pane_id":"w9N:p2"}}}`}
		}
		return Result{Stdout: `{"id":"cli","result":{"agent":{"agent_status":"idle"}}}`}
	}}
}

var linkedOpts = SpawnOpts{Slot: "w1", Cwd: "/repo/.worktrees/w1", ConfigDir: "/acct/one", Launch: "claude --model sonnet",
	Env: []string{"ROTA_SLOT=w1", "ROTA_PORT_BASE=20000", "ROTA_PORT_BLOCK=100", "ROTA_DB_SUFFIX=_w1"}, BootTimeout: 60, LinkWorkspace: true}

func TestHerdrSpawnLinkedWorkspace(t *testing.T) {
	f := linkedFake("false", nil)
	got, err := New("herdr", deps(f, herdrEnv, &clock{})).Spawn(bg, linkedOpts)
	if err != nil || got != "w9N:t2" {
		t.Fatalf("Spawn = %q, %v", got, err)
	}
	want := []string{
		"herdr worktree open --workspace w9 --path /repo/.worktrees/w1 --label w1 --no-focus",
		"herdr tab create --workspace w9N --cwd /repo/.worktrees/w1 --label w1 --no-focus --env CLAUDE_CONFIG_DIR=/acct/one --env ROTA_SLOT=w1 --env ROTA_PORT_BASE=20000 --env ROTA_PORT_BLOCK=100 --env ROTA_DB_SUFFIX=_w1",
		"herdr tab close w9N:t1",
		"herdr agent start rota-w1-62ee7678 --kind claude --pane w9N:p2 --timeout 60000 -- --model sonnet",
	}
	if !reflect.DeepEqual(f.calls, want) {
		t.Errorf("calls =\n%s\nwant\n%s", strings.Join(f.calls, "\n"), strings.Join(want, "\n"))
	}
}

func TestHerdrSpawnLinkedLeavesAnAlreadyOpenRootTab(t *testing.T) {
	f := linkedFake("true", nil)
	if _, err := New("herdr", deps(f, herdrEnv, &clock{})).Spawn(bg, linkedOpts); err != nil {
		t.Fatal(err)
	}
	if f.count("herdr tab close") != 0 {
		t.Errorf("closed a tab rota did not make:\n%s", f.log())
	}
}

func TestHerdrSpawnLinkedFailures(t *testing.T) {
	bad := Result{ExitCode: 1, Stderr: herdrErr("boom")}
	for name, tc := range map[string]struct {
		fail   map[string]Result
		opts   func(*SpawnOpts)
		closed string
	}{
		"open fails":       {fail: map[string]Result{"worktree open": bad}},
		"relative path":    {opts: func(o *SpawnOpts) { o.Cwd = ".worktrees/w1" }},
		"tab create fails": {fail: map[string]Result{"tab create": bad}, closed: "herdr workspace close w9N"},
	} {
		f := linkedFake("false", tc.fail)
		o := linkedOpts
		if tc.opts != nil {
			tc.opts(&o)
		}
		if _, err := New("herdr", deps(f, herdrEnv, &clock{})).Spawn(bg, o); err == nil {
			t.Errorf("%s: want an error", name)
		}
		if got := f.count("herdr workspace close"); (tc.closed != "") != (got == 1) || strings.Contains(f.log(), "--group") {
			t.Errorf("%s: log =\n%s", name, f.log())
		}
		if tc.closed != "" && !strings.Contains(f.log(), tc.closed) {
			t.Errorf("%s: want %q in\n%s", name, tc.closed, f.log())
		}
	}
}

func TestHerdrSpawnDefaultStaysATab(t *testing.T) {
	f := linkedFake("false", nil)
	o := linkedOpts
	o.LinkWorkspace = false
	if _, err := New("herdr", deps(f, herdrEnv, &clock{})).Spawn(bg, o); err != nil {
		t.Fatal(err)
	}
	if f.count("herdr worktree") != 0 || !strings.Contains(f.calls[0], "tab create --workspace w9 ") {
		t.Errorf("log =\n%s", f.log())
	}
}

// Kill closes a slot workspace by id once it holds no tab, never the
// orchestrator's workspace and never with --group.
func TestHerdrKillClosesAnEmptySlotWorkspaceByID(t *testing.T) {
	for name, tc := range map[string]struct {
		handle, tabs string
		want         int
	}{
		"empty slot workspace":    {"w9N:t2", "0", 1},
		"slot workspace has tabs": {"w9N:t2", "1", 0},
		"orchestrator workspace":  {"w9:t7", "0", 0},
	} {
		f := &fake{handler: func(_ string, a []string) Result {
			switch a[0] + " " + a[1] {
			case "tab get":
				return Result{ExitCode: 1, Stderr: herdrErr("tab_not_found")}
			case "workspace get":
				return Result{Stdout: `{"id":"cli","result":{"workspace":{"tab_count":` + tc.tabs + `}}}`}
			}
			return Result{Stdout: agentJSON("idle")}
		}}
		if err := New("herdr", deps(f, herdrEnv, &clock{})).Kill(bg, "w1", tc.handle); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if f.count("herdr workspace close") != tc.want || strings.Contains(f.log(), "--group") {
			t.Errorf("%s: log =\n%s", name, f.log())
		}
		if tc.want == 1 && !strings.Contains(f.log(), "herdr workspace close w9N") {
			t.Errorf("%s: log =\n%s", name, f.log())
		}
	}
}

// The operator tab and the round token belong to the orchestrator's own
// workspace, linked slots or not.
func TestHerdrOperatorTabAndRoundTokenStayInTheOrchestratorWorkspace(t *testing.T) {
	f := &fake{handler: func(_ string, a []string) Result {
		return Result{Stdout: tabCreated}
	}}
	h := New("herdr", deps(f, herdrEnv, &clock{}))
	if _, err := h.(TabOpener).OpenTab(bg, TabOpts{Label: "op", Cwd: "/repo", Command: "x"}); err != nil {
		t.Fatal(err)
	}
	h.(Labeler).LabelWorkspace(bg, "r19")
	for _, w := range []string{"herdr tab create --workspace w9 --cwd /repo", "herdr workspace report-metadata w9 --source rota --token round=r19"} {
		if !strings.Contains(f.log(), w) {
			t.Errorf("want %q in\n%s", w, f.log())
		}
	}
}
