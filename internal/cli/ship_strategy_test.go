package cli

import (
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
)

func TestShipStrategyReadsConfigOnce(t *testing.T) {
	root := trackerProject(t, "")
	cfg, err := jsonx.Decode([]byte(`{"issues":{"source":"file"},"work":{"mergeStrategy":"direct"}}`))
	if err != nil {
		t.Fatal(err)
	}
	d := originDeps("git@github.com:o/r.git")
	d.Getwd = func() (string, error) { return root, nil }
	reads := 0
	d.Config = func(string) any {
		reads++
		return cfg
	}
	res, err := shipStrategy(nil)(&Ctx{Deps: d}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := res.Data.(*jsonx.Object).Get("strategy"); got != "direct" {
		t.Errorf("strategy = %v, want direct from config", got)
	}
	if reads != 1 {
		t.Errorf("config reads = %d, want one config snapshot per strategy call", reads)
	}
}

// rota ship strategy wires the backend, the resolved forge and work.mergeStrategy
// into ship.Strategy, which owns the rule (see its tests). These cases cover the
// wiring: forge resolution from the origin, the config key, the exit code.
func TestShipStrategy(t *testing.T) {
	const gh, gl = "git@github.com:o/r.git", "https://gitlab.com/o/r"
	for _, c := range []struct {
		name, cfg, origin string
		want, forge       string
		code              int
	}{
		{"file, pr, github origin", `{"issues":{"source":"file"},"work":{"mergeStrategy":"pr"}}`, gh, "pr", "github", 0},
		{"file, pr, no remote is local", `{"issues":{"source":"file"},"work":{"mergeStrategy":"pr"}}`, "", "direct", "local", 0},
		{"tracker source forces pr", `{"forge":{"provider":"gitlab"},"issues":{"source":"gitlab"},"work":{"mergeStrategy":"direct"}}`, gl, "pr", "gitlab", 0},
		{"tracker source on a local forge cannot open a PR", `{"issues":{"source":"github"}}`, "", "", "local", ExitResolution},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := trackerProject(t, c.cfg)
			code, env, stderr := rotaRunWith(t, originDeps(c.origin), "--json", "-C", root, "ship", "strategy")
			if code != c.code {
				t.Fatalf("exit %d, want %d: %v %s", code, c.code, env, stderr)
			}
			if c.code != 0 {
				return
			}
			d := dataOf(env)
			if get(d, "strategy") != c.want || get(d, "forge") != c.forge {
				t.Errorf("%v, want strategy %s forge %s", env, c.want, c.forge)
			}
		})
	}
}
