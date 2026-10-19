package host

import (
	"reflect"
	"strings"
	"testing"
)

func TestHerdrLayoutPanesJoinTabAndWorkspaceLabels(t *testing.T) {
	f := &fake{handler: func(_ string, args []string) Result {
		switch strings.Join(args, " ") {
		case "pane list":
			return Result{Stdout: `{"result":{"type":"pane_list","panes":[
				{"pane_id":"w1:p1","tab_id":"w1:t1","workspace_id":"w1","cwd":"/p","foreground_cwd":"/p/x","agent":"claude"},
				{"pane_id":"w1:p2","tab_id":"w1:t2","workspace_id":"w1","cwd":"/p/.worktrees/ben"}]}}`}
		case "tab list":
			return Result{Stdout: `{"result":{"type":"tab_list","tabs":[{"tab_id":"w1:t1","label":"orchestrator"},{"tab_id":"w1:t2","label":"ben"}]}}`}
		case "workspace list":
			return Result{Stdout: `{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"w1","label":"rota"}]}}`}
		}
		return Result{ExitCode: 1}
	}}
	got, err := New("herdr", deps(f, nil, &clock{})).(Layouter).LayoutPanes(bg)
	if err != nil {
		t.Fatal(err)
	}
	want := []LayoutPane{
		{ID: "w1:p1", Tab: "w1:t1", Workspace: "w1", TabLabel: "orchestrator", WorkspaceName: "rota", Cwd: "/p", FgCwd: "/p/x", Agent: "claude"},
		{ID: "w1:p2", Tab: "w1:t2", Workspace: "w1", TabLabel: "ben", WorkspaceName: "rota", Cwd: "/p/.worktrees/ben"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("panes = %+v", got)
	}
}

func TestHerdrPaneRects(t *testing.T) {
	f := &fake{handler: func(_ string, args []string) Result {
		if strings.Join(args, " ") != "pane layout --pane w1:p1" {
			return Result{ExitCode: 1}
		}
		return Result{Stdout: `{"result":{"layout":{"panes":[
			{"pane_id":"w1:p1","rect":{"x":0,"y":0,"width":30,"height":40}},
			{"pane_id":"w1:p2","rect":{"x":30,"y":20,"width":90,"height":20}}]}}}`}
	}}
	got, err := New("herdr", deps(f, nil, &clock{})).(Layouter).PaneRects(bg, "w1:p1")
	if err != nil || got["w1:p2"] != (Rect{30, 20, 90, 20}) || len(got) != 2 {
		t.Errorf("rects = %+v, %v", got, err)
	}
}

func TestHerdrPaneMoveCommands(t *testing.T) {
	f := &fake{handler: func(string, []string) Result { return Result{} }}
	l := New("herdr", deps(f, nil, &clock{})).(Layouter)
	if err := l.SplitInto(bg, "w1:p2", "w1:t1", "w1:p1", "right", 1.0/3); err != nil {
		t.Fatal(err)
	}
	if err := l.ToNewTab(bg, "w1:p2", "ben"); err != nil {
		t.Fatal(err)
	}
	if err := l.RenameTab(bg, "w1:t1", "rota"); err != nil {
		t.Fatal(err)
	}
	if err := l.RenamePane(bg, "w1:p1", "orchestrator"); err != nil {
		t.Fatal(err)
	}
	want := "herdr pane move w1:p2 --tab w1:t1 --split right --target-pane w1:p1 --ratio 0.3333 --no-focus\n" +
		"herdr pane move w1:p2 --new-tab --label ben --no-focus\n" +
		"herdr tab rename w1:t1 rota\n" +
		"herdr pane rename w1:p1 orchestrator"
	if f.log() != want {
		t.Errorf("commands:\n%s", f.log())
	}
	bad := &fake{handler: func(string, []string) Result { return Result{ExitCode: 1, Stderr: "no"} }}
	if New("herdr", deps(bad, nil, &clock{})).(Layouter).ToNewTab(bg, "p", "x") == nil {
		t.Error("a failing move must be an error")
	}
}

func TestFormatRatio(t *testing.T) {
	for in, want := range map[float64]string{0.5: "0.5", 1.0 / 3: "0.3333", 0.25: "0.25", 1.0 / 7: "0.1429", 1: "1"} {
		if got := FormatRatio(in); got != want {
			t.Errorf("FormatRatio(%v) = %s, want %s", in, got, want)
		}
	}
}

func TestHerdrOpenLinkedAndMoveIntoItsWorkspace(t *testing.T) {
	f := linkedFake("false", nil)
	l := New("herdr", deps(f, herdrEnv, &clock{})).(LinkedLayouter)
	ws, root, err := l.OpenLinked(bg, "w9", "ben", "/repo/.worktrees/ben")
	if err != nil || ws != "w9N" || root != "w9N:t1" {
		t.Fatalf("OpenLinked = %q, %q, %v", ws, root, err)
	}
	if err := l.ToNewTabIn(bg, "w9:p4", ws, "ben"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"herdr worktree open --workspace w9 --path /repo/.worktrees/ben --label ben --no-focus",
		"herdr pane move w9:p4 --new-tab --workspace w9N --label ben --no-focus",
	}
	if !reflect.DeepEqual(f.calls, want) {
		t.Errorf("calls = %v", f.calls)
	}
}

// An already open workspace has no root tab of rota's to close.
func TestHerdrOpenLinkedReportsNoRootTabWhenAlreadyOpen(t *testing.T) {
	f := linkedFake("true", nil)
	_, root, err := New("herdr", deps(f, herdrEnv, &clock{})).(LinkedLayouter).OpenLinked(bg, "w9", "ben", "/repo/.worktrees/ben")
	if err != nil || root != "" {
		t.Errorf("root = %q, %v", root, err)
	}
}

func TestHerdrOpenLinkedRefusesARelativePath(t *testing.T) {
	f := linkedFake("false", nil)
	if _, _, err := New("herdr", deps(f, herdrEnv, &clock{})).(LinkedLayouter).OpenLinked(bg, "w9", "ben", ".worktrees/ben"); err == nil || !strings.Contains(err.Error(), "absolute") || len(f.calls) != 0 {
		t.Errorf("err = %v, calls = %v", err, f.calls)
	}
}

// AC-3: a slot is found by its agent name, which derives from slot and the
// recorded handle, so every pane move that renumbers the pane (split, tabs,
// split) leaves Status and PaneOf, what round status, watch and poll read,
// resolving each slot with the handle it was dispatched with.
func TestHerdrFindsEverySlotAfterPanesChangeWorkspace(t *testing.T) {
	pane := map[string]string{AgentName("ben", "w9N:t2"): "w9N:p1", AgentName("kit", "w9P:t2"): "w9P:p1"}
	f := &fake{handler: func(_ string, a []string) Result {
		switch {
		case a[0] == "agent" && a[1] == "get":
			p, ok := pane[a[2]]
			if !ok {
				return Result{ExitCode: 1, Stderr: herdrErr("agent_not_found")}
			}
			return Result{Stdout: `{"result":{"agent":{"pane_id":"` + p + `","agent_status":"working"}}}`}
		case a[0] == "pane" && a[1] == "move":
			// A move across workspaces gives the pane a new id.
			for name, p := range pane {
				if p == a[2] {
					pane[name] = p + "x"
				}
			}
		}
		return Result{Stdout: `{"result":{}}`}
	}}
	h := New("herdr", deps(f, herdrEnv, &clock{}))
	l := h.(Layouter)
	handles := map[string]string{"ben": "w9N:t2", "kit": "w9P:t2"}
	check := func(step string) {
		t.Helper()
		for slot, handle := range handles {
			if got := l.PaneOf(bg, slot, handle); got != pane[AgentName(slot, handle)] || got == "" {
				t.Errorf("%s: PaneOf(%s) = %q", step, slot, got)
			}
			if got := h.Status(bg, slot, handle); got != "working" {
				t.Errorf("%s: Status(%s) = %q", step, slot, got)
			}
		}
	}
	check("start")
	for _, step := range []string{"split", "tabs", "split"} {
		for _, slot := range []string{"ben", "kit"} {
			l.ToNewTab(bg, l.PaneOf(bg, slot, handles[slot]), slot)
		}
		check(step)
	}
}
