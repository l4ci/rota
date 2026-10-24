package cli

import (
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/migrate"
	"github.com/l4ci/rota/internal/tracker"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/stale"
)

// goldenDay is the day the goldens in testdata/golden were recorded from the
// retired helpers. The knowledge, glossary, decisions and migrate verbs stamp
// the day on what they write, so testDeps pins Deps.Today to that day.
const goldenDay = "2026-10-03"

// TestMain puts tripwire gh and glab
// first on PATH: issue mode builds the real tracker unless a test injects one,
// and no test may reach a forge.
func TestMain(m *testing.M) {
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
	for _, k := range []string{"TMUX", "TMUX_PANE", "HERDR_ENV", "HERDR_PANE_ID", "HERDR_WORKSPACE_ID", "HERDR_SOCKET_PATH", "ROTA_TEST_LOCK_HELD"} {
		os.Unsetenv(k)
	}
	// Retry and bulk-write pauses would only slow the tests down.
	tracker.RetryPause = 0
	migrate.BulkPace = 0
	// `rota init` registers the project in the global registry (#24): no test
	// may write the developer's real ~/.local/state/rota.
	os.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg"))
	os.Setenv("XDG_STATE_HOME", filepath.Join(dir, "xstate"))
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
	d.RecordMistakes = false
	pinDay(d, goldenDay)
	// doctor's disk check reads the real volume: pin a healthy one so a full
	// developer disk does not add a line to every doctor golden (#85).
	d.DoctorDisk = doctorDisk("50:100")
	d.Host = func(kind host.Kind) host.Host {
		if kind == "herdr" {
			return &absentHerdr{}
		}
		return &limFake{}
	}
	return d
}

// pinNow fixes the clock Deps.Now reads (RFC 3339). Today stays as pinned.
func pinNow(d *Deps, rfc3339 string) {
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		panic(err)
	}
	d.Now = func() time.Time { return t }
}

// pinDay fixes the day Deps.Today reads (YYYY-MM-DD).
func pinDay(d *Deps, day string) {
	t, ok := stale.ParseDate(day)
	if !ok {
		panic("bad day " + day)
	}
	d.Today = func() time.Time { return t }
}

// pinHolder names pid as the process holding the round lease.
func pinHolder(d *Deps, pid int) { d.HolderPID = func() int { return pid } }

// dirPins are per-project Deps pins: a helper that builds a project (orchProject)
// registers them, and ONLY rotaIn/rotaStdin (via depsIn) apply them, and only
// when they run in exactly that dir. Helpers that take explicit Deps (rotaInWith,
// rotaRunWith, runMain, runMainWith, mainWith(testDeps())) and runs in a subdir
// of the pinned dir get the real clock and holder with no error: pass
// depsIn(dir) as their Deps, or pin the Deps yourself.
// Keys are unique temp dirs, removed at test cleanup.
var dirPins sync.Map // dir -> []func(*Deps)

// pinDir adds pins to rotaIn/rotaStdin runs in dir (see dirPins for what it misses).
func pinDir(t *testing.T, dir string, pins ...func(*Deps)) {
	t.Helper()
	old, _ := dirPins.Load(dir)
	prev, _ := old.([]func(*Deps))
	dirPins.Store(dir, append(slices.Clone(prev), pins...))
	t.Cleanup(func() { dirPins.Delete(dir) })
}

// depsIn is testDeps with the pins registered for dir.
func depsIn(dir string) *Deps {
	d := testDeps()
	v, _ := dirPins.Load(dir)
	pins, _ := v.([]func(*Deps))
	for _, p := range pins {
		p(d)
	}
	return d
}

// absentHerdr is herdr on a machine without it: Require fails, so the verbs
// that need it report it unavailable instead of reaching a real one.
type absentHerdr struct{ cliHost }

func (*absentHerdr) Kind() host.Kind { return host.Herdr }

func (a *absentHerdr) Capabilities() host.Capabilities { return host.CapabilitiesOf(a) }
func (*absentHerdr) Require() error                    { return errors.New("herdr is not installed") }

// mainWith is Main with the given Deps.
func mainWith(d *Deps, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return run(Tree(), d, args, stdin, stdout, stderr)
}
