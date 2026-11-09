package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// A project or local config that does not parse, or is not an object, is the
// user's file: every verb that loads the merged config exits 5 naming it, not
// 0 on defaults (#2935).
func TestVerbsRefuseCorruptProjectOrLocalConfig(t *testing.T) {
	for _, file := range []string{"config.json", "config.local.json"} {
		for _, body := range []string{"{oops\n", "[1]\n"} {
			t.Run(file+body, func(t *testing.T) {
				root := trackerProject(t, "{}\n")
				write(t, filepath.Join(root, ".rota", file), body)
				for _, argv := range [][]string{{"backlog", "list"}, {"doctor"}, {"round", "status"}} {
					code, _, stderr := rotaRun(t, append([]string{"--json", "-C", root}, argv...)...)
					if code != ExitUnavailable || !strings.Contains(stderr, file) {
						t.Errorf("%v: code=%d stderr=%s", argv, code, stderr)
					}
					if strings.Contains(stderr, "bug in rota") {
						t.Errorf("%v: a corrupt config is not a rota bug: %s", argv, stderr)
					}
				}
			})
		}
	}
}

// A blank file still reads as {}, and `config` verbs stay usable to repair it.
func TestVerbsAcceptBlankConfigAndConfigRepairs(t *testing.T) {
	root := trackerProject(t, "  \n")
	write(t, filepath.Join(root, ".rota", "config.local.json"), "")
	if code, _, stderr := rotaRun(t, "--json", "-C", root, "backlog", "list"); code != 0 {
		t.Errorf("blank config: code=%d %s", code, stderr)
	}
	write(t, filepath.Join(root, ".rota", "config.json"), "{oops\n")
	if code, _, stderr := rotaRun(t, "--json", "-C", root, "config", "show", "docs.path"); code != ExitUnavailable {
		t.Errorf("config show keeps its own refusal: code=%d %s", code, stderr)
	}
}
