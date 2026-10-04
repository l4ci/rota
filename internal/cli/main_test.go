package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/knowledge"
)

// goldenDay is the day the goldens in testdata/golden were recorded from the
// retired helpers. The knowledge, glossary, decisions and migrate verbs stamp
// knowledge.Today on what they write, so the tests pin it to that day.
const goldenDay = "2026-10-03"

// TestMain pins knowledge.Today to goldenDay and puts tripwire gh and glab
// first on PATH: issue mode builds the real tracker unless a test injects one,
// and no test may reach a forge.
func TestMain(m *testing.M) {
	knowledge.Today = func() string { return goldenDay }
	dir, err := os.MkdirTemp("", "cli-tripwire")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	hit := filepath.Join(dir, "hit")
	for _, name := range []string{"gh", "glab", "codex"} {
		script := fmt.Sprintf("#!/bin/sh\necho \"$0 $*\" >> '%s'\nexit 99\n", hit)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	// The usage-limit watcher (D3) types into panes: no test may reach the
	// real tmux or herdr this round runs in. The default host is a fake that
	// records, and the pane variables are cleared.
	for _, k := range []string{"TMUX", "TMUX_PANE", "HERDR_ENV", "HERDR_PANE_ID", "HERDR_WORKSPACE_ID", "HERDR_SOCKET_PATH"} {
		os.Unsetenv(k)
	}
	limitHost = func(string) host.Host { return &limFake{} }
	code := m.Run()
	if b, err := os.ReadFile(hit); err == nil {
		fmt.Fprintf(os.Stderr, "FAIL: tests exec'd the forge CLI from PATH:\n%s", b)
		code = 1
	}
	os.RemoveAll(dir)
	os.Exit(code)
}
