// Package testiso keeps a test binary off the developer's real global rota
// config. Import it blank from a package whose tests load config: its init
// points ROTA_CONFIG_DIR at an empty temp dir before any test runs, replacing
// whatever the environment exported. A test that needs a global config sets
// ROTA_CONFIG_DIR itself with t.Setenv.
package testiso

import "os"

func init() {
	dir, err := os.MkdirTemp("", "rota-test-global")
	if err != nil {
		panic(err)
	}
	os.Setenv("ROTA_CONFIG_DIR", dir)
}
