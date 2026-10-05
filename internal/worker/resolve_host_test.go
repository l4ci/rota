package worker

import (
	"errors"
	"testing"
)

// TestResolveHostCrossesTheSeam is the one table for "which host does this
// round run on" as every verb sees it: recorded host x work.dispatch x env.
func TestResolveHostCrossesTheSeam(t *testing.T) {
	herdrBin := func(string) (string, error) { return "/bin/herdr", nil }
	noBin := func(string) (string, error) { return "", errors.New("not found") }
	cases := []struct {
		name, registry, dispatch string
		env                      map[string]string
		look                     func(string) (string, error)
		want, wantPane           string
	}{
		{"recorded herdr beats tmux dispatch", "herdr", "tmux", nil, noBin, "herdr", "herdr"},
		{"recorded tmux beats herdr pane", "tmux", "", map[string]string{"HERDR_ENV": "1"}, herdrBin, "tmux", "tmux"},
		{"recorded solo stays solo", "solo", "herdr", nil, noBin, "solo", "solo"},
		{"no round, herdr dispatch", "", "herdr", nil, noBin, "herdr", "herdr"},
		{"no round, herdr pane", "", "", map[string]string{"HERDR_ENV": "1"}, herdrBin, "herdr", "herdr"},
		{"no round, tmux", "", "", map[string]string{"TMUX": "x"}, noBin, "tmux", "tmux"},
		{"no round, bare env", "", "", nil, noBin, "solo", "tmux"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := `{}`
			if c.dispatch != "" {
				cfg = `{"work":{"dispatch":"` + c.dispatch + `"}}`
			}
			dir := newProject(t, cfg)
			if c.registry != "" {
				goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
				recordHost(t, dir, c.registry)
			}
			getenv := func(k string) string { return c.env[k] }
			if got := ResolveHost(dir, getenv, c.look); got != c.want {
				t.Errorf("ResolveHost = %q, want %q", got, c.want)
			}
			if got := ResolvePaneHost(dir, getenv, c.look); got != c.wantPane {
				t.Errorf("ResolvePaneHost = %q, want %q", got, c.wantPane)
			}
		})
	}
}
