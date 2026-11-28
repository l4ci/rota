package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
)

// One rule for every project-layer reader and writer: a blank file reads as {},
// a file that does not parse or is not an object is refused untouched (#4426).
func TestBlankConfigReadsByOneRule(t *testing.T) {
	// project is a support clone, the one shape setup acts on, with body
	// written over the named layer.
	project := func(t *testing.T, file, body string) (string, string) {
		isolateGlobal(t)
		dir, _ := supportClone(t)
		path := filepath.Join(dir, ".rota", file)
		write(t, path, body)
		return dir, path
	}
	verbs := []struct {
		name, file string
		argv       []string
	}{
		{"check", "config.json", []string{"config", "check"}},
		{"fill", "config.json", []string{"config", "fill"}},
		{"init", "config.json", []string{"init"}},
		{"set", "config.json", []string{"config", "set", "work.isolation", "worktree"}},
		{"set autonomy", "config.json", []string{"config", "set", "autonomy.level", "auto"}},
		{"save global", "config.json", []string{"config", "save-global"}},
		{"set local", "config.local.json", []string{"config", "set", "--local", "round.role", "support"}},
		{"setup support", "config.local.json", []string{"setup", "--yes", "--set", "round.role=support"}},
		{"role gate", "config.local.json", []string{"config", "fill"}},
	}
	run := func(t *testing.T, dir string, argv []string) (int, string) {
		if argv[0] == "setup" {
			code, out, errOut := setupRun(t, dir, "", false, argv[1:]...)
			return code, out + errOut
		}
		code, _, errOut := rotaRun(t, append([]string{"--json", "-C", dir}, argv...)...)
		return code, errOut
	}
	for _, v := range verbs {
		t.Run(v.name+" blank", func(t *testing.T) {
			dir, _ := project(t, v.file, " \n")
			code, out := run(t, dir, v.argv)
			// check answers 1 for a stale config, which a {} one is.
			if want := map[bool]int{true: 1, false: 0}[v.name == "check"]; code != want {
				t.Errorf("exit %d, want %d: %s", code, want, out)
			}
		})
		for _, body := range []string{"{oops\n", "[1]\n"} {
			t.Run(v.name+" "+body, func(t *testing.T) {
				dir, path := project(t, v.file, body)
				code, out := run(t, dir, v.argv)
				if code == 0 {
					t.Errorf("exit 0, want a refusal: %s", out)
				}
				if got, err := os.ReadFile(path); err != nil || string(got) != body {
					t.Errorf("file = %q, %v; want it untouched", got, err)
				}
			})
		}
	}
}

// A blank config.json is stale, not corrupt and not fresh (#4426).
func TestConfigCheckBlankIsStale(t *testing.T) {
	isolateGlobal(t)
	root := trackerProject(t, "{}")
	write(t, filepath.Join(root, ".rota", "config.json"), "")
	_, env, stderr := rotaRun(t, "--json", "-C", root, "config", "check")
	data, _ := env["data"].(*jsonx.Object)
	if data == nil {
		t.Fatalf("no data: %s", stderr)
	}
	if got, _ := data.Get("status"); got != "stale" {
		t.Errorf("status = %v, want stale: %s", got, stderr)
	}
}
