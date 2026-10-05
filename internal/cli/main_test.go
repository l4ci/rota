package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/host"
)

// goldenDay is the day the goldens in testdata/golden were recorded from the
// retired helpers. The knowledge, glossary, decisions and migrate verbs stamp
// the day on what they write, so the tests pin ROTA_TEST_TODAY to that day.
const goldenDay = "2026-10-03"

// TestMain pins ROTA_TEST_TODAY to goldenDay and puts tripwire gh and glab
// first on PATH: issue mode builds the real tracker unless a test injects one,
// and no test may reach a forge.
func TestMain(m *testing.M) {
	os.Setenv("ROTA_TEST_TODAY", goldenDay)
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
	// `rota init` registers the project in the global registry (#24): no test
	// may write the developer's real ~/.config/rota.
	os.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg"))
	// doctor's disk check reads the real volume: pin a healthy one so a full
	// developer disk does not add a line to every doctor golden (#85).
	os.Setenv("ROTA_TEST_DOCTOR_DISK", "50:100")
	// Fixture repos commit and merge: the identity comes from here, not from
	// the developer's (or CI's missing) global git config. t@x, not gittest's
	// default: the goldens hold commit hashes made under it.
	gittest.SetIdentity("GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@x", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@x")
	code := m.Run()
	if b, err := os.ReadFile(hit); err == nil {
		fmt.Fprintf(os.Stderr, "FAIL: tests exec'd the forge CLI from PATH:\n%s", b)
		code = 1
	}
	os.RemoveAll(dir)
	os.Exit(code)
}

// testDeps is what a test's invocation reaches: the real machine, except the
// usage-limit watcher's host, which is a fake that records. A test swaps a
// field on its own copy and runs through mainWith, so nothing leaks across
// tests.
func testDeps() *Deps {
	d := defaultDeps()
	d.Host = func(kind string) host.Host {
		if kind == "herdr" {
			return &absentHerdr{}
		}
		return &limFake{}
	}
	return d
}

// absentHerdr is herdr on a machine without it: Require fails, so the verbs
// that need it report it unavailable instead of reaching a real one.
type absentHerdr struct{ cliHost }

func (*absentHerdr) Name() string   { return "herdr" }
func (*absentHerdr) Require() error { return errors.New("herdr is not installed") }

// mainWith is Main with the given Deps.
func mainWith(d *Deps, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return run(Tree(), d, args, stdin, stdout, stderr)
}
