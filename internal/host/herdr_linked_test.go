package host

import (
	"bytes"
	"errors"
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

var linkedOpts = SpawnOpts{Slot: "w1", Cwd: "/repo/.worktrees/w1", Launch: "claude --model sonnet",
	Env: []string{"CLAUDE_CONFIG_DIR=/acct/one", "ROTA_SLOT=w1", "ROTA_PORT_BASE=20000", "ROTA_PORT_BLOCK=100", "ROTA_DB_SUFFIX=_w1"}, BootTimeout: 60, LinkWorkspace: true}

func TestHerdrSpawnLinkedWorkspace(t *testing.T) {
	f := linkedFake("false", nil)
	o := linkedOpts
	var linked bool
	o.Linked = &linked
	got, err := New("herdr", deps(f, herdrEnv, &clock{})).Spawn(bg, o)
	if err != nil || got != "w9N:t2" || !linked {
		t.Fatalf("Spawn = %q, %v, linked %v", got, err, linked)
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

// CloseWorkspace closes a recorded slot workspace by id once it holds no tab,
// never one that still has a tab, never with --group.
func TestHerdrCloseWorkspace(t *testing.T) {
	for name, tc := range map[string]struct {
		get  Result
		want int
	}{
		"empty":     {Result{Stdout: `{"id":"cli","result":{"workspace":{"tab_count":0}}}`}, 1},
		"has a tab": {Result{Stdout: `{"id":"cli","result":{"workspace":{"tab_count":1}}}`}, 0},
		"gone":      {Result{ExitCode: 1, Stderr: herdrErr("workspace_not_found")}, 0},
	} {
		f := &fake{handler: func(_ string, a []string) Result {
			if a[1] == "get" {
				return tc.get
			}
			return Result{Stdout: `{"id":"cli","result":{"type":"ok"}}`}
		}}
		if err := New("herdr", deps(f, herdrEnv, &clock{})).(WorkspaceCloser).CloseWorkspace(bg, "w9N"); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if f.count("herdr workspace close") != tc.want || strings.Contains(f.log(), "--group") {
			t.Errorf("%s: log =\n%s", name, f.log())
		}
	}
}

func TestHerdrCloseWorkspaceNeverTheOrchestratorsOwn(t *testing.T) {
	f := &fake{handler: func(string, []string) Result { return Result{} }}
	if err := New("herdr", deps(f, herdrEnv, &clock{})).(WorkspaceCloser).CloseWorkspace(bg, "w9"); err == nil {
		t.Error("want a refusal")
	}
	if len(f.calls) != 0 {
		t.Errorf("ran %v", f.calls)
	}
}

func TestHerdrKillLeavesWorkspacesAlone(t *testing.T) {
	f := linkedFake("false", map[string]Result{"tab get": {ExitCode: 1, Stderr: herdrErr("tab_not_found")}})
	if err := New("herdr", deps(f, herdrEnv, &clock{})).Kill(bg, "w1", "w9N:t2"); err != nil {
		t.Fatal(err)
	}
	if f.count("herdr workspace") != 0 {
		t.Errorf("log =\n%s", f.log())
	}
}

func TestHerdrSpawnLinkedBadReplies(t *testing.T) {
	bad := Result{ExitCode: 1, Stderr: herdrErr("boom")}
	for name, tc := range map[string]struct {
		fail     map[string]Result
		wantErr  string
		wantCall string // a call that must have run
		noCall   string // a call that must not have run
	}{
		"open without a workspace id": {map[string]Result{"worktree open": {Stdout: `{"result":{"already_open":false}}`}}, "no workspace id", "", "tab create"},
		"tab create without ids":      {map[string]Result{"tab create": {Stdout: `{"result":{}}`}}, "no tab/pane id", "herdr workspace close w9N", ""},
		"root tab will not close":     {map[string]Result{"tab close": bad}, "root tab w9N:t1", "", "agent start"},
		"agent start fails":           {map[string]Result{"agent start": {ExitCode: 1, Stderr: herdrErr("boom")}}, "agent start failed", "herdr tab close w9N:t2", ""},
	} {
		f := linkedFake("false", tc.fail)
		_, err := New("herdr", deps(f, herdrEnv, &clock{})).Spawn(bg, linkedOpts)
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.wantErr)
		}
		if tc.wantCall != "" && !strings.Contains(f.log(), tc.wantCall) {
			t.Errorf("%s: want %q in\n%s", name, tc.wantCall, f.log())
		}
		if tc.noCall != "" && strings.Contains(f.log(), tc.noCall) {
			t.Errorf("%s: ran %q:\n%s", name, tc.noCall, f.log())
		}
	}
}

func TestHerdrSpawnLinkedReportsAnOrphanWorkspace(t *testing.T) {
	bad := Result{ExitCode: 1, Stderr: herdrErr("boom")}
	f := linkedFake("false", map[string]Result{"tab create": bad, "workspace close": bad})
	var errw bytes.Buffer
	d := deps(f, herdrEnv, &clock{})
	d.Stderr = &errw
	if _, err := New("herdr", d).Spawn(bg, linkedOpts); err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(errw.String(), "w9N") {
		t.Errorf("stderr = %q, want the workspace id", errw.String())
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

// A herdr without `worktree open` (or its flags) is not a failed dispatch: the
// slot is a tab in the caller's workspace, with one warning and nothing linked.
func TestHerdrSpawnLinkedFallsBackToATabWhenOpenIsUnsupported(t *testing.T) {
	for name, bad := range map[string]Result{
		"unknown method": {ExitCode: 1, Stderr: herdrErr("unknown_method")},
		"unknown flag":   {ExitCode: 2, Stderr: "error: unexpected argument '--no-focus' found"},
	} {
		f := linkedFake("false", map[string]Result{"worktree open": bad})
		var errb bytes.Buffer
		d := deps(f, herdrEnv, &clock{})
		d.Stderr = &errb
		o := linkedOpts
		var linked bool
		o.Linked = &linked
		got, err := New("herdr", d).Spawn(bg, o)
		if err != nil || got != "w9N:t2" {
			t.Fatalf("%s: Spawn = %q, %v", name, got, err)
		}
		if linked || !strings.Contains(f.log(), "herdr tab create --workspace w9 ") || f.count("herdr tab close") != 0 {
			t.Errorf("%s: linked = %v, log =\n%s", name, linked, f.log())
		}
		if want := "herdr cannot link workspaces; slot w1 stays a tab"; !strings.Contains(errb.String(), want) {
			t.Errorf("%s: stderr = %q", name, errb.String())
		}
	}
}

// Any other open failure still fails the dispatch.
func TestHerdrSpawnLinkedOtherOpenErrorsStillFail(t *testing.T) {
	f := linkedFake("false", map[string]Result{"worktree open": {ExitCode: 1, Stderr: herdrErr("worktree_not_found")}})
	if _, err := New("herdr", deps(f, herdrEnv, &clock{})).Spawn(bg, linkedOpts); err == nil || f.count("herdr tab create") != 0 {
		t.Errorf("err = %v, log =\n%s", err, f.log())
	}
}

func TestHerdrOpenLinkedAndMoveReportAnUnsupportedHerdr(t *testing.T) {
	bad := Result{ExitCode: 1, Stderr: herdrErr("unknown_method")}
	f := linkedFake("false", map[string]Result{"worktree open": bad, "pane move": bad})
	l := New("herdr", deps(f, herdrEnv, &clock{})).(LinkedLayouter)
	if _, _, err := l.OpenLinked(bg, "w9", "ben", "/repo/.worktrees/ben"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("OpenLinked err = %v", err)
	}
	if err := l.ToNewTabIn(bg, "w9:p4", "w9N", "ben"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("ToNewTabIn err = %v", err)
	}
	other := linkedFake("false", map[string]Result{"worktree open": {ExitCode: 1, Stderr: herdrErr("boom")}})
	if _, _, err := New("herdr", deps(other, herdrEnv, &clock{})).(LinkedLayouter).OpenLinked(bg, "w9", "ben", "/x"); errors.Is(err, ErrUnsupported) {
		t.Error("an ordinary failure was called unsupported")
	}
}
