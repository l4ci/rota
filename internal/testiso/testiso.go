// Package testiso keeps a test binary off the developer's real global rota
// config. Import it blank from a package whose tests load config: its init
// drops an exported ROTA_CONFIG_DIR and points XDG_CONFIG_HOME at an empty temp
// dir before any test runs. A test that needs a global config sets
// ROTA_CONFIG_DIR (or XDG_CONFIG_HOME) itself with t.Setenv.
package testiso

import "os"

func init() {
	dir, err := os.MkdirTemp("", "rota-test-global")
	if err != nil {
		panic(err)
	}
	os.Unsetenv("ROTA_CONFIG_DIR")
	os.Setenv("XDG_CONFIG_HOME", dir)
}
