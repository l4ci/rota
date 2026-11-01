package host

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestResolve(t *testing.T) {
	has := func(names ...string) func(string) (string, error) {
		return func(n string) (string, error) {
			for _, h := range names {
				if h == n {
					return "/bin/" + n, nil
				}
			}
			return "", errors.New("not found")
		}
	}
	cases := []struct {
		name     string
		registry Kind
		dispatch Kind
		env      map[string]string
		path     func(string) (string, error)
		want     Kind
	}{
		{"explicit herdr, nothing present", "", "herdr", nil, has(), "herdr"},
		{"explicit tmux inside herdr", "", "tmux", map[string]string{"HERDR_ENV": "1"}, has("herdr"), "tmux"},
		{"explicit herdr inside tmux", "", "herdr", map[string]string{"TMUX": "/tmp/x"}, has(), "herdr"},
		{"registry herdr beats tmux dispatch", "herdr", "tmux", nil, has(), "herdr"},
		{"registry tmux beats herdr env", "tmux", "", map[string]string{"HERDR_ENV": "1"}, has("herdr"), "tmux"},
		{"registry solo beats tmux env", Solo, "tmux", map[string]string{"TMUX": "x"}, has(), Solo},
		{"registry herdr, nothing present", "herdr", "", nil, has(), "herdr"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Resolve(c.registry, c.dispatch, func(k string) string { return c.env[k] }, c.path)
			if got != c.want {
				t.Fatalf("Resolve(%q, %q, %v) = %q, want %q", c.registry, c.dispatch, c.env, got, c.want)
			}
		})
	}
}

// Launch keeps its own precedence and may open a session outside a pane.
func TestResolveLaunch(t *testing.T) {
	has := func(names ...string) func(string) (string, error) {
		return func(n string) (string, error) {
			for _, h := range names {
				if h == n {
					return "/bin/" + n, nil
				}
			}
			return "", errors.New("not found")
		}
	}
	herdrPane := map[string]string{"HERDR_ENV": "1", "HERDR_WORKSPACE_ID": "w1"}
	tmuxPane := map[string]string{"TMUX": "/tmp/t,1,0"}
	cases := []struct {
		name      string
		dispatch  Kind
		env       map[string]string
		installed []string
		launch    Launch // ResolveLaunch(dispatch, ...)
		fails     bool   // ResolveLaunch errors
	}{
		{"no pane, herdr installed", "", nil, []string{"herdr"}, Launch{Herdr, true}, false},
		{"no pane, tmux installed", "", nil, []string{"tmux"}, Launch{Tmux, true}, false},
		{"no pane, both installed", "", nil, []string{"herdr", "tmux"}, Launch{Herdr, true}, false},
		{"no pane, nothing installed", "", nil, nil, Launch{Solo, false}, false},
		{"no pane, subagent, herdr installed", "subagent", nil, []string{"herdr"}, Launch{Herdr, true}, false},
		{"no pane, dispatch tmux, both installed", "tmux", nil, []string{"herdr", "tmux"}, Launch{Tmux, true}, false},
		{"no pane, dispatch herdr, both installed", "herdr", nil, []string{"herdr", "tmux"}, Launch{Herdr, true}, false},
		{"no pane, dispatch herdr, not installed", "herdr", nil, []string{"tmux"}, Launch{}, true},
		{"no pane, dispatch tmux, not installed", "tmux", nil, nil, Launch{}, true},
		{"TMUX", "", tmuxPane, []string{"tmux"}, Launch{Tmux, false}, false},
		{"TMUX, dispatch herdr", "herdr", tmuxPane, []string{"herdr", "tmux"}, Launch{Tmux, false}, false},
		{"herdr pane, dispatch tmux", "tmux", herdrPane, []string{"herdr", "tmux"}, Launch{Herdr, false}, false},
		{"herdr pane", "", herdrPane, []string{"herdr", "tmux"}, Launch{Herdr, false}, false},
		{"herdr pane, binary missing, tmux installed", "", herdrPane, []string{"tmux"}, Launch{Tmux, true}, false},
		{"HERDR_ENV without workspace id", "", map[string]string{"HERDR_ENV": "1"}, []string{"herdr"}, Launch{Herdr, true}, false},
		{"herdr and tmux panes", "", map[string]string{"HERDR_ENV": "1", "HERDR_WORKSPACE_ID": "w1", "TMUX": "x"}, []string{"herdr", "tmux"}, Launch{Herdr, false}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			getenv := func(k string) string { return c.env[k] }
			got, err := ResolveLaunch(c.dispatch, getenv, has(c.installed...))
			if (err != nil) != c.fails {
				t.Fatalf("ResolveLaunch err = %v, want failure %v", err, c.fails)
			}
			if err == nil && got != c.launch {
				t.Errorf("ResolveLaunch = %+v, want %+v", got, c.launch)
			}
		})
	}
}

// Both entry points use the same pane facts, but deliberately apply different
// rules: Resolve needs the marker and binary, InSession the marker and workspace.
func TestHerdrPaneCallers(t *testing.T) {
	cases := []struct {
		name, marker, workspace string
		withBinary              Kind
		inSession               bool
	}{
		{"no marker or workspace", "", "", Solo, false},
		{"workspace only", "", "w1", Solo, false},
		{"wrong marker", "0", "", Solo, false},
		{"wrong marker with workspace", "0", "w1", Solo, false},
		{"marker only", "1", "", Herdr, false},
		{"marker and workspace", "1", "w1", Herdr, true},
	}
	for _, c := range cases {
		for _, installed := range []bool{false, true} {
			for _, tmux := range []string{"", "x"} {
				for _, dispatch := range []Kind{"", "subagent"} {
					name := fmt.Sprintf("%s/binary=%t/tmux=%t/dispatch=%s", c.name, installed, tmux != "", dispatch)
					t.Run(name, func(t *testing.T) {
						env := map[string]string{"HERDR_ENV": c.marker, "HERDR_WORKSPACE_ID": c.workspace, "TMUX": tmux}
						d := Deps{
							Getenv: func(k string) string { return env[k] },
							LookPath: func(n string) (string, error) {
								if n == "herdr" && installed {
									return "/bin/herdr", nil
								}
								return "", errors.New("not found")
							},
							Run: func(context.Context, string, []string) (Result, error) {
								t.Fatal("pane detection must not run commands")
								return Result{}, nil
							},
						}
						want := Solo
						if installed {
							want = c.withBinary
						}
						if want == Solo && tmux != "" {
							want = Tmux
						}
						if got := Resolve("", dispatch, d.Getenv, d.LookPath); got != want {
							t.Errorf("Resolve = %q, want %q", got, want)
						}
						if got := New(Herdr, d).InSession(); got != c.inSession {
							t.Errorf("InSession = %t, want %t", got, c.inSession)
						}
					})
				}
			}
		}
	}
}
