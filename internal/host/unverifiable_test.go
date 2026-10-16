package host

import (
	"errors"
	"strings"
	"testing"
)

// Absence of an answer is not absence of a session (#820): only herdr's own
// agent_not_found proves the agent gone.
func TestHerdrStatusSeparatesGoneFromUnanswered(t *testing.T) {
	for name, tc := range map[string]struct {
		res  Result
		want string
	}{
		"agent gone":      {Result{ExitCode: 1, Stderr: herdrErr("agent_not_found")}, "gone"},
		"tab handle gone": {Result{ExitCode: 1, Stderr: herdrErr("tab_not_found")}, "unverifiable:stale-handle"},
		"pane gone":       {Result{ExitCode: 1, Stderr: herdrErr("pane_not_found")}, "unverifiable:stale-handle"},
		"no server":       {Result{ExitCode: 1, Stderr: "could not connect to herdr socket"}, "unverifiable:host-unreachable"},
		"not runnable":    {Result{ExitCode: 127, Stderr: "exec: herdr: not found"}, "unverifiable:host-unreachable"},
		"working":         {Result{Stdout: agentJSON("working")}, "working"},
	} {
		t.Run(name, func(t *testing.T) {
			f := &fake{handler: func(string, []string) Result { return tc.res }}
			h := New("herdr", deps(f, herdrEnv, &clock{}))
			if got := h.Status(bg, "w1", "w9:t7"); got != tc.want {
				t.Errorf("Status = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReadPaneReportsFailureApartFromEmptyText(t *testing.T) {
	for _, kind := range []Kind{Herdr, Tmux} {
		ok := &fake{handler: func(string, []string) Result { return Result{} }}
		bad := &fake{handler: func(string, []string) Result { return Result{ExitCode: 1, Stderr: "boom"} }}
		for name, f := range map[string]*fake{"empty": ok, "failing": bad} {
			r := New(kind, deps(f, herdrEnv, &clock{})).Capabilities().PaneReader
			if r == nil {
				t.Errorf("%s host has no ReadPane", kind)
				continue
			}
			text, err := r.ReadPane(bg, "w1", "rota:w1", 40)
			if (err != nil) != (name == "failing") || strings.TrimSpace(text) != "" {
				t.Errorf("%s %s: ReadPane = %q, %v", kind, name, text, err)
			}
		}
	}
}

// tmux says why a capture failed on stderr. A window, pane or server that is
// gone is proof (ErrPaneGone); any other failure is just unreadable (#820).
func TestTmuxReadPaneTellsAGonePaneFromAnUnreadableOne(t *testing.T) {
	for stderr, gone := range map[string]bool{
		"can't find window: w1":                       true,
		"can't find pane: %3":                         true,
		"no server running on /tmp/tmux-1000/default": true,
		"error connecting to /tmp/tmux-1000/default (No such file or directory)": true,
		"capture-pane: out of memory":                                            false,
	} {
		f := &fake{handler: func(string, []string) Result { return Result{ExitCode: 1, Stderr: stderr} }}
		_, err := New(Tmux, deps(f, nil, &clock{})).Capabilities().PaneReader.ReadPane(bg, "w1", "rota:w1", 40)
		if err == nil || errors.Is(err, ErrPaneGone) != gone {
			t.Errorf("%q: err = %v, want gone=%v", stderr, err, gone)
		}
	}
}
