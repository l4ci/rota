package host

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// One answer policy serves herdr and tmux: three startup dialogs (theme,
// trust, bypass), and a pane unchanged since the last answer is a slow redraw.
func TestSpawnBootDialogPolicy(t *testing.T) {
	const limit = 3
	trust := readFixture(t, "trust-dialog-2.1.288.txt")
	hosts := []struct {
		name    string
		env     map[string]string
		handler func(pane func() string, a []string) Result
		answers func(*fake) int
		failure string
	}{
		{"herdr", herdrEnv, func(pane func() string, a []string) Result {
			switch a[0] + " " + a[1] {
			case "tab create":
				return Result{Stdout: tabCreated}
			case "agent start":
				return Result{ExitCode: 1, Stderr: herdrErr("agent_not_ready")}
			case "agent read":
				return Result{Stdout: pane()}
			case "agent wait":
				return Result{Stdout: agentJSON("blocked")}
			}
			return Result{}
		}, func(f *fake) int { return f.count("herdr agent send-keys") },
			"did not reach idle after its startup dialogs"},
		{"tmux", nil, func(pane func() string, a []string) Result {
			if a[0] == "capture-pane" {
				return Result{Stdout: pane()}
			}
			return Result{}
		}, (*fake).answers, "Accessing workspace:"},
	}
	cases := []struct {
		name  string
		fresh bool // every read shows a new dialog
		want  int
	}{
		{"fresh dialogs stop at the limit", true, limit},
		{"an unchanged pane is answered once", false, 1},
	}
	for _, h := range hosts {
		for _, c := range cases {
			t.Run(h.name+"/"+c.name, func(t *testing.T) {
				reads := 0
				pane := func() string {
					if !c.fresh {
						return trust
					}
					reads++
					return trust + fmt.Sprintf("\n\nredraw %d\n", reads)
				}
				f := &fake{handler: func(_ string, a []string) Result { return h.handler(pane, a) }}
				_, err := New(Kind(h.name), deps(f, h.env, &clock{})).Spawn(bg, SpawnOpts{Slot: "w1", Session: "rota", Cwd: "/wt", Launch: "claude", BootTimeout: 20})
				if err == nil || !strings.Contains(err.Error(), h.failure) {
					t.Errorf("err = %v, want %q", err, h.failure)
				}
				var de *DialogError
				if h.name == "tmux" && (!errors.Is(err, ErrDialogOpen) || !errors.As(err, &de)) {
					t.Errorf("err = %v, want a *DialogError", err)
				}
				if n := h.answers(f); n != c.want {
					t.Errorf("answers = %d, want %d:\n%s", n, c.want, f.log())
				}
			})
		}
	}
}
