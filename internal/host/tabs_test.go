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

func TestHerdrCloseTabVerification(t *testing.T) {
	const down = "host transport unreachable"
	cases := []struct {
		name    string
		get     Result
		wantErr string // "" = proven gone
	}{
		{"native not-found proves gone", Result{ExitCode: 1, Stderr: herdrErr("tab_not_found")}, ""},
		{"still open", Result{}, "still open after close"},
		{"transport failure is unproven", Result{ExitCode: 1, Stderr: down}, "could not be verified"},
		{"other herdr error is unproven", Result{ExitCode: 1, Stderr: herdrErr("internal_error")}, "could not be verified"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fake{handler: func(_ string, a []string) Result {
				if a[0] == "tab" && a[1] == "get" {
					return c.get
				}
				return Result{}
			}}
			err := New("herdr", deps(f, nil, &clock{})).(TabLister).CloseTab(bg, "w2:t1")
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("CloseTab = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("CloseTab = %v, want error containing %q", err, c.wantErr)
			}
		})
	}
}

func TestHerdrCloseTabCloseFailure(t *testing.T) {
	f := &fake{handler: func(_ string, a []string) Result {
		if a[1] == "close" {
			return Result{ExitCode: 1, Stderr: "host transport unreachable"}
		}
		return Result{ExitCode: 1, Stderr: herdrErr("tab_not_found")}
	}}
	if err := New("herdr", deps(f, nil, &clock{})).(TabLister).CloseTab(bg, "w2:t1"); err == nil {
		t.Error("a failed close must be an error")
	}
}
