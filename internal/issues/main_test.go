package issues

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMain puts tripwire gh and glab first on PATH: every test injects its
// executor, so any real forge CLI call fails the run.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "issues-tripwire")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	hit := filepath.Join(dir, "hit")
	for _, cli := range []string{"gh", "glab"} {
		script := fmt.Sprintf("#!/bin/sh\necho \"$0 $*\" >> '%s'\nexit 99\n", hit)
		if err := os.WriteFile(filepath.Join(dir, cli), []byte(script), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	code := m.Run()
	if b, err := os.ReadFile(hit); err == nil {
		fmt.Fprintf(os.Stderr, "FAIL: tests exec'd the forge CLI from PATH:\n%s", b)
		code = 1
	}
	os.RemoveAll(dir)
	os.Exit(code)
}
