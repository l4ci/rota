package host

import (
	"strings"
	"testing"
)

func TestHerdrOpenTabRunsCommandInRootPane(t *testing.T) {
	f := &fake{handler: func(_ string, args []string) Result {
		if args[0] == "tab" && args[1] == "create" {
			return Result{Stdout: `{"result":{"tab":{"tab_id":"w1:t9"},"root_pane":{"pane_id":"w1:p9"}}}`}
		}
		return Result{}
	}}
	h := New("herdr", deps(f, map[string]string{"HERDR_WORKSPACE_ID": "w1"}, &clock{}))
	tab, err := h.(TabOpener).OpenTab(bg, TabOpts{Label: "orchestrator", Cwd: "/p", Command: "rota keepalive run -- claude"})
	if err != nil || tab != "w1:t9" {
		t.Fatalf("tab = %q, err = %v", tab, err)
	}
	log := f.log()
	for _, want := range []string{
		"herdr tab create --workspace w1 --cwd /p --label orchestrator --focus",
		"herdr pane run w1:p9 rota keepalive run -- claude",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("missing call %q in:\n%s", want, log)
		}
	}
	if strings.Contains(log, "agent start") {
		t.Errorf("the supervisor is not an agent binary, agent start must not run:\n%s", log)
	}
}

func TestHerdrOpenTabRetriesPaneRace(t *testing.T) {
	runs := 0
	f := &fake{handler: func(_ string, args []string) Result {
		switch {
		case args[0] == "tab" && args[1] == "create":
			return Result{Stdout: `{"result":{"tab":{"tab_id":"t"},"root_pane":{"pane_id":"p"}}}`}
		case args[0] == "pane" && args[1] == "run":
			if runs++; runs < 3 {
				return Result{ExitCode: 1, Stderr: `{"error":{"code":"pane_not_found"}}`}
			}
		}
		return Result{}
	}}
	c := &clock{}
	h := New("herdr", deps(f, nil, c))
	if _, err := h.(TabOpener).OpenTab(bg, TabOpts{Label: "o", Cwd: "/p", Command: "x"}); err != nil {
		t.Fatal(err)
	}
	if runs != 3 || c.slept == 0 {
		t.Errorf("runs = %d, slept = %v", runs, c.slept)
	}
}

func TestHerdrOpenTabClosesTabWhenRunFails(t *testing.T) {
	f := &fake{handler: func(_ string, args []string) Result {
		switch {
		case args[0] == "tab" && args[1] == "create":
			return Result{Stdout: `{"result":{"tab":{"tab_id":"t"},"root_pane":{"pane_id":"p"}}}`}
		case args[0] == "pane":
			return Result{ExitCode: 1, Stderr: "boom"}
		}
		return Result{}
	}}
	_, err := New("herdr", deps(f, nil, &clock{})).(TabOpener).OpenTab(bg, TabOpts{Label: "o", Cwd: "/p", Command: "x"})
	if err == nil || f.count("herdr tab close t") != 1 {
		t.Fatalf("err = %v, calls:\n%s", err, f.log())
	}
	if f.count("herdr pane run") != 1 {
		t.Errorf("a non-race failure must not retry:\n%s", f.log())
	}
}

func TestTmuxOpenTabTypesCommandIntoNewWindow(t *testing.T) {
	f := &fake{handler: func(_ string, args []string) Result {
		if args[0] == "new-window" {
			return Result{Stdout: "@5\n"}
		}
		return Result{}
	}}
	win, err := New("tmux", deps(f, nil, &clock{})).(TabOpener).OpenTab(bg, TabOpts{Label: "orchestrator", Cwd: "/p", Command: "cmd"})
	if err != nil || win != "@5" {
		t.Fatalf("win = %q, err = %v", win, err)
	}
	log := f.log()
	for _, want := range []string{"tmux new-window -P -F #{window_id} -n orchestrator -c /p", "tmux send-keys -t @5 cmd C-m"} {
		if !strings.Contains(log, want) {
			t.Errorf("missing %q in:\n%s", want, log)
		}
	}
}
