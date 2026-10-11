package host

import (
	"errors"
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
		registry string
		dispatch string
		env      map[string]string
		path     func(string) (string, error)
		want     string
	}{
		{"explicit herdr, nothing present", "", "herdr", nil, has(), "herdr"},
		{"explicit tmux inside herdr", "", "tmux", map[string]string{"HERDR_ENV": "1"}, has("herdr"), "tmux"},
		{"explicit herdr inside tmux", "", "herdr", map[string]string{"TMUX": "/tmp/x"}, has(), "herdr"},
		{"unset, nothing", "", "", nil, has(), Solo},
		{"subagent, nothing", "", "subagent", nil, has("herdr"), Solo},
		{"unset, herdr pane with binary", "", "", map[string]string{"HERDR_ENV": "1"}, has("herdr"), "herdr"},
		{"subagent, herdr pane with binary", "", "subagent", map[string]string{"HERDR_ENV": "1"}, has("herdr"), "herdr"},
		{"herdr pane, binary missing, no tmux", "", "", map[string]string{"HERDR_ENV": "1"}, has(), Solo},
		{"herdr pane, binary missing, tmux set", "", "", map[string]string{"HERDR_ENV": "1", "TMUX": "x"}, has(), "tmux"},
		{"herdr binary but not in a pane", "", "", nil, has("herdr"), Solo},
		{"HERDR_ENV not 1", "", "", map[string]string{"HERDR_ENV": "0"}, has("herdr"), Solo},
		{"tmux set", "", "", map[string]string{"TMUX": "/tmp/tmux-1/default,1,0"}, has(), "tmux"},
		{"herdr wins over tmux", "", "subagent", map[string]string{"HERDR_ENV": "1", "TMUX": "x"}, has("herdr"), "herdr"},
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

// TestResolveAndResolveLaunchOnOneTable runs both modes over the same
// no-pane, TMUX, HERDR_ENV and work.dispatch combinations, so where they part
// is written down: the registry-less in-session rule (Resolve) answers solo
// outside a pane where the launcher (ResolveLaunch) opens a session.
func TestResolveAndResolveLaunchOnOneTable(t *testing.T) {
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
		dispatch  string
		env       map[string]string
		installed []string
		resolve   string // Resolve("", dispatch, ...)
		launch    Launch // ResolveLaunch(dispatch, ...)
		fails     bool   // ResolveLaunch errors
	}{
		{"no pane, herdr installed", "", nil, []string{"herdr"}, Solo, Launch{Herdr, true}, false},
		{"no pane, tmux installed", "", nil, []string{"tmux"}, Solo, Launch{Tmux, true}, false},
		{"no pane, both installed", "", nil, []string{"herdr", "tmux"}, Solo, Launch{Herdr, true}, false},
		{"no pane, nothing installed", "", nil, nil, Solo, Launch{Solo, false}, false},
		{"no pane, subagent, herdr installed", "subagent", nil, []string{"herdr"}, Solo, Launch{Herdr, true}, false},
		{"no pane, dispatch tmux, both installed", "tmux", nil, []string{"herdr", "tmux"}, Tmux, Launch{Tmux, true}, false},
		{"no pane, dispatch herdr, both installed", "herdr", nil, []string{"herdr", "tmux"}, Herdr, Launch{Herdr, true}, false},
		{"no pane, dispatch herdr, not installed", "herdr", nil, []string{"tmux"}, Herdr, Launch{}, true},
		{"no pane, dispatch tmux, not installed", "tmux", nil, nil, Tmux, Launch{}, true},
		{"TMUX", "", tmuxPane, []string{"tmux"}, Tmux, Launch{Tmux, false}, false},
		{"TMUX, dispatch herdr", "herdr", tmuxPane, []string{"herdr", "tmux"}, Herdr, Launch{Tmux, false}, false},
		{"herdr pane", "", herdrPane, []string{"herdr", "tmux"}, Herdr, Launch{Herdr, false}, false},
		{"herdr pane, dispatch tmux", "tmux", herdrPane, []string{"herdr", "tmux"}, Tmux, Launch{Herdr, false}, false},
		{"herdr pane, binary missing, tmux installed", "", herdrPane, []string{"tmux"}, Solo, Launch{Tmux, true}, false},
		{"HERDR_ENV without workspace id", "", map[string]string{"HERDR_ENV": "1"}, []string{"herdr"}, Herdr, Launch{Herdr, true}, false},
		{"herdr and tmux panes", "", map[string]string{"HERDR_ENV": "1", "HERDR_WORKSPACE_ID": "w1", "TMUX": "x"}, []string{"herdr", "tmux"}, Herdr, Launch{Herdr, false}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			getenv := func(k string) string { return c.env[k] }
			if got := Resolve("", c.dispatch, getenv, has(c.installed...)); got != c.resolve {
				t.Errorf("Resolve = %q, want %q", got, c.resolve)
			}
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
