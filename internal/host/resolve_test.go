package host

import (
	"errors"
	"testing"
)

func TestResolveRound(t *testing.T) {
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
		dispatch string
		env      map[string]string
		path     func(string) (string, error)
		want     string
	}{
		{"explicit herdr, nothing present", "herdr", nil, has(), "herdr"},
		{"explicit tmux inside herdr", "tmux", map[string]string{"HERDR_ENV": "1"}, has("herdr"), "tmux"},
		{"explicit herdr inside tmux", "herdr", map[string]string{"TMUX": "/tmp/x"}, has(), "herdr"},
		{"unset, nothing", "", nil, has(), Solo},
		{"subagent, nothing", "subagent", nil, has("herdr"), Solo},
		{"unset, herdr pane with binary", "", map[string]string{"HERDR_ENV": "1"}, has("herdr"), "herdr"},
		{"subagent, herdr pane with binary", "subagent", map[string]string{"HERDR_ENV": "1"}, has("herdr"), "herdr"},
		{"herdr pane, binary missing, no tmux", "", map[string]string{"HERDR_ENV": "1"}, has(), Solo},
		{"herdr pane, binary missing, tmux set", "", map[string]string{"HERDR_ENV": "1", "TMUX": "x"}, has(), "tmux"},
		{"herdr binary but not in a pane", "", nil, has("herdr"), Solo},
		{"HERDR_ENV not 1", "", map[string]string{"HERDR_ENV": "0"}, has("herdr"), Solo},
		{"tmux set", "", map[string]string{"TMUX": "/tmp/tmux-1/default,1,0"}, has(), "tmux"},
		{"herdr wins over tmux", "subagent", map[string]string{"HERDR_ENV": "1", "TMUX": "x"}, has("herdr"), "herdr"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ResolveRound(c.dispatch, func(k string) string { return c.env[k] }, c.path)
			if got != c.want {
				t.Fatalf("ResolveRound(%q, %v) = %q, want %q", c.dispatch, c.env, got, c.want)
			}
		})
	}
}
