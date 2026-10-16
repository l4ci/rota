package host

import (
	"context"
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

type paneReader interface {
	ReadPane(ctx context.Context, slot, handle string, lines int) (string, error)
}

func TestReadPaneReportsFailureApartFromEmptyText(t *testing.T) {
	for _, kind := range []Kind{Herdr, Tmux} {
		ok := &fake{handler: func(string, []string) Result { return Result{} }}
		bad := &fake{handler: func(string, []string) Result { return Result{ExitCode: 1, Stderr: "boom"} }}
		for name, f := range map[string]*fake{"empty": ok, "failing": bad} {
			r, isReader := New(kind, deps(f, herdrEnv, &clock{})).(paneReader)
			if !isReader {
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
