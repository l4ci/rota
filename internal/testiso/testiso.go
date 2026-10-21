// Package testiso keeps a test binary off the developer's real global rota
// config. Import it blank from a package whose tests load config: its init
// drops an exported ROTA_CONFIG_DIR and points XDG_CONFIG_HOME at a path that
// does not exist, so the global config loads as absent (a no-op by design).
// The path hangs off a private MkdirTemp dir that is removed at once, so the
// name is unguessable (no pre-planted config in shared tmp) and nothing leaks.
// A test that needs a global config sets ROTA_CONFIG_DIR (or XDG_CONFIG_HOME)
// itself with t.Setenv.
package testiso

import (
	"os"
	"path/filepath"
)

func init() {
	d, err := os.MkdirTemp("", "rota-test-no-global-")
	if err != nil {
		panic(err)
	}
	os.Remove(d)
	os.Unsetenv("ROTA_CONFIG_DIR")
	os.Setenv("XDG_CONFIG_HOME", filepath.Join(d, "nonexistent"))
}
