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
	want := "herdr pane move w1:p2 --tab w1:t1 --split right --target-pane w1:p1 --ratio 0.3333 --no-focus\n" +
		"herdr pane move w1:p2 --new-tab --label ben --no-focus"
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
