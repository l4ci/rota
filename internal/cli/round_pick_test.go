package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRoundPickUsage(t *testing.T) {
	deps := testDeps()
	dir := workerProject(t, `{}`)
	empty := filepath.Join(t.TempDir(), "reason.md")
	if err := os.WriteFile(empty, []byte(" \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string][]string{
		"no item":        {"round", "pick", "--pr", "7", "--reason-file", empty},
		"no pr":          {"round", "pick", "404", "--reason-file", empty},
		"no reason file": {"round", "pick", "404", "--pr", "7"},
		"empty reason":   {"round", "pick", "404", "--pr", "7", "--reason-file", empty},
	} {
		if code, _, errOut := rotaInWith(t, deps, dir, args...); code != 2 {
			t.Errorf("%s: exit %d, want 2: %s", name, code, errOut)
		}
	}
}
