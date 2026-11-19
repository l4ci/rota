// Package testiso keeps a test binary off the developer's real global rota
// config. Import it blank from a package whose tests load config: its init
// drops an exported ROTA_CONFIG_DIR and points XDG_CONFIG_HOME at a path that
// does not exist, so the global config loads as absent (a no-op by design).
// The path hangs off a private MkdirTemp dir that is removed at once, so the
// name is unguessable (no pre-planted config in shared tmp) and nothing leaks.
// A test that needs a global config sets ROTA_CONFIG_DIR (or XDG_CONFIG_HOME)
// itself with t.Setenv.
//
// The same init scrubs the host-session variables (HERDR_*, HC_*, ORCA_*, TMUX*) and the harness config dirs (CLAUDE_CONFIG_DIR, CODEX_HOME) by prefix,
// so a test never inherits the herdr or tmux pane a round runs in. Tripwire
// (tripwire.go) puts fake host binaries first on PATH for TestMains.
//
// It also sets GIT_CEILING_DIRECTORIES to the temp root. A worker slot's TMPDIR
// sits inside the main checkout, so a test project under t.TempDir() would let
// git walk up to the real checkout and write its .rota/ (#3478, #3508). Git
// never looks at or above a ceiling, so a worktree cwd outside it is unaffected.
package testiso

import (
	"os"
	"path/filepath"

	"github.com/l4ci/rota/internal/hostenv"
)

func init() {
	hostenv.Scrub()
	os.Setenv("GIT_CEILING_DIRECTORIES", os.TempDir())
	d, err := os.MkdirTemp("", "rota-test-no-global-")
	if err != nil {
		panic(err)
	}
	os.Remove(d)
	os.Unsetenv("ROTA_CONFIG_DIR")
	os.Setenv("XDG_CONFIG_HOME", filepath.Join(d, "nonexistent"))
}
