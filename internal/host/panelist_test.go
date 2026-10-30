package host

import (
	"reflect"
	"strings"
	"testing"
)

func TestHerdrListPanesDecodesOnce(t *testing.T) {
	f := &fake{handler: func(_ string, args []string) Result {
		if strings.Join(args, " ") == "pane list" {
			return Result{Stdout: `{"result":{"panes":[
				{"pane_id":"w1:p1","tab_id":"w1:t1","workspace_id":"w1","cwd":"/a","foreground_cwd":"/b","agent":"claude","agent_status":"idle"}]}}`}
		}
		return Result{ExitCode: 1}
	}}
	h := New("herdr", deps(f, nil, &clock{})).(*herdr)
	got, err := h.listPanes(bg)
	if err != nil {
		t.Fatal(err)
	}
	want := []herdrPane{{PaneID: "w1:p1", TabID: "w1:t1", WorkspaceID: "w1", Cwd: "/a", FgCwd: "/b", Agent: "claude", Status: "idle"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("panes = %+v", got)
	}
	bad := &fake{handler: func(string, []string) Result { return Result{Stdout: "nope"} }}
	if _, err := New("herdr", deps(bad, nil, &clock{})).(*herdr).listPanes(bg); err == nil {
		t.Error("an unreadable reply must be an error")
	}
	down := &fake{handler: func(string, []string) Result { return Result{ExitCode: 1, Stderr: "down"} }}
	if _, err := New("herdr", deps(down, nil, &clock{})).(*herdr).listPanes(bg); err == nil {
		t.Error("a failing herdr must be an error")
	}
}

func TestHerdrAgentlessPredicate(t *testing.T) {
	f := &fake{handler: func(_ string, args []string) Result {
		switch strings.Join(args, " ") {
		case "pane process-info --pane shell":
			return Result{Stdout: `{"result":{"process_info":{"foreground_processes":[{"name":"zsh"}]}}}`}
		case "pane process-info --pane prog":
			return Result{Stdout: `{"result":{"process_info":{"foreground_processes":[{"name":"claude"}]}}}`}
		}
		return Result{ExitCode: 1}
	}}
	h := New("herdr", deps(f, nil, &clock{})).(*herdr)
	for name, c := range map[string]struct {
		p    herdrPane
		want bool
	}{
		"named agent":        {herdrPane{PaneID: "shell", Agent: "claude"}, false},
		"known status":       {herdrPane{PaneID: "shell", Status: "idle"}, false},
		"shell only":         {herdrPane{PaneID: "shell", Status: "unknown"}, true},
		"empty status":       {herdrPane{PaneID: "shell"}, true},
		"other program":      {herdrPane{PaneID: "prog", Status: "unknown"}, false},
		"unreadable process": {herdrPane{PaneID: "gone", Status: "unknown"}, false},
	} {
		if got := h.agentless(bg, c.p); got != c.want {
			t.Errorf("%s: agentless = %v", name, got)
		}
	}
}
