package host

import (
	"reflect"
	"strings"
	"testing"
)

func TestHerdrTabsProveAgentless(t *testing.T) {
	f := &fake{handler: func(_ string, args []string) Result {
		switch strings.Join(args, " ") {
		case "pane list":
			return Result{Stdout: `{"result":{"panes":[
				{"pane_id":"w1:p1","tab_id":"w1:t1","cwd":"/p/.worktrees/a","agent":"claude","agent_status":"idle"},
				{"pane_id":"w2:p1","tab_id":"w2:t1","cwd":"/p/.worktrees/b","agent_status":"unknown"},
				{"pane_id":"w3:p1","tab_id":"w3:t1","cwd":"/p/.worktrees/c","agent_status":"unknown"},
				{"pane_id":"w4:p1","tab_id":"w4:t1","cwd":"/p/.worktrees/d","agent_status":"unknown"}]}}`}
		case "pane process-info --pane w2:p1":
			return Result{Stdout: `{"result":{"process_info":{"foreground_processes":[{"name":"zsh"}]}}}`}
		case "pane process-info --pane w3:p1": // an unrecognised program may be an agent
			return Result{Stdout: `{"result":{"process_info":{"foreground_processes":[{"name":"claude"}]}}}`}
		}
		return Result{ExitCode: 1} // w4: process-info unreadable
	}}
	got, err := New("herdr", deps(f, nil, &clock{})).(TabLister).Tabs(bg)
	if err != nil {
		t.Fatal(err)
	}
	want := []Tab{
		{ID: "w1:t1", Cwds: []string{"/p/.worktrees/a"}},
		{ID: "w2:t1", Cwds: []string{"/p/.worktrees/b"}, Agentless: true},
		{ID: "w3:t1", Cwds: []string{"/p/.worktrees/c"}},
		{ID: "w4:t1", Cwds: []string{"/p/.worktrees/d"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tabs = %+v", got)
	}
}

func TestHerdrTabsFailuresAreErrors(t *testing.T) {
	f := &fake{handler: func(string, []string) Result { return Result{ExitCode: 1, Stderr: "down"} }}
	if _, err := New("herdr", deps(f, nil, &clock{})).(TabLister).Tabs(bg); err == nil {
		t.Error("a failing herdr must be an error")
	}
}

func TestHerdrCloseTabProvesGone(t *testing.T) {
	gone := true
	f := &fake{handler: func(_ string, args []string) Result {
		if args[0] == "tab" && args[1] == "get" && !gone {
			return Result{}
		}
		if args[1] == "get" {
			return Result{ExitCode: 1}
		}
		return Result{}
	}}
	h := New("herdr", deps(f, nil, &clock{})).(TabLister)
	if err := h.CloseTab(bg, "w2:t1"); err != nil {
		t.Errorf("close: %v", err)
	}
	gone = false
	if err := h.CloseTab(bg, "w2:t1"); err == nil {
		t.Error("a tab that is still open is a failure")
	}
}
