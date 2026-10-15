package cli

import (
	"testing"
)

// rota ship strategy answers how the branch lands: a local forge has no PR, so
// it is always direct; a tracker source needs a PR; otherwise work.mergeStrategy.
func TestShipStrategy(t *testing.T) {
	const gh, gl = "git@github.com:o/r.git", "https://gitlab.com/o/r"
	for _, c := range []struct {
		name, cfg, origin string
		want, forge       string
		code              int
	}{
		{"file, pr, github", `{"issues":{"source":"file"},"work":{"mergeStrategy":"pr"}}`, gh, "pr", "github", 0},
		{"file, direct, github", `{"issues":{"source":"file"},"work":{"mergeStrategy":"direct"}}`, gh, "direct", "github", 0},
		{"file, unset, github", `{"issues":{"source":"file"}}`, gh, "unset", "github", 0},
		{"file, pr, no remote is local", `{"issues":{"source":"file"},"work":{"mergeStrategy":"pr"}}`, "", "direct", "local", 0},
		{"file, pr, forge.provider local", `{"forge":{"provider":"local"},"work":{"mergeStrategy":"pr"}}`, gh, "direct", "local", 0},
		{"file, unset, no remote", `{}`, "", "direct", "local", 0},
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
