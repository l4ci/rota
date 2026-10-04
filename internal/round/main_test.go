package round

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMain puts tripwire codex, herdr and tmux first on PATH. A round runs
// inside herdr and a codex worker's preflight looks codex up on PATH: any test
// that reaches a real binary fails the whole run instead of running it.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "round-tripwire")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	hit := filepath.Join(dir, "hit")
	for _, name := range []string{"codex", "herdr", "tmux"} {
		script := fmt.Sprintf("#!/bin/sh\necho \"$0 $*\" >> '%s'\nexit 99\n", hit)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	code := m.Run()
	if b, err := os.ReadFile(hit); err == nil {
		fmt.Fprintf(os.Stderr, "FAIL: tests exec'd a host or codex binary from PATH:\n%s", b)
		code = 1
	}
	os.RemoveAll(dir)
	os.Exit(code)
}
