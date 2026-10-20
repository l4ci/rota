// Package testiso keeps a test binary off the developer's real global rota
// config. Import it blank from a package whose tests load config: its init
// drops an exported ROTA_CONFIG_DIR and points XDG_CONFIG_HOME at a path that
// does not exist, so the global config loads as absent (a no-op by design).
// Nothing is created on disk, so nothing leaks. A test that needs a global
// config sets ROTA_CONFIG_DIR (or XDG_CONFIG_HOME) itself with t.Setenv.
package testiso

import (
	"fmt"
	"os"
	"path/filepath"
)

func init() {
	os.Unsetenv("ROTA_CONFIG_DIR")
	os.Setenv("XDG_CONFIG_HOME", filepath.Join(os.TempDir(), fmt.Sprintf("rota-test-no-global-%d", os.Getpid()), "nonexistent"))
}
