package config

import (
	"fmt"
	"os"
	"testing"
)

// TestMain points the global config dir at an empty temp dir, so no test reads
// the developer's real ~/.config/rota/config.json.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "config-global")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Setenv("ROTA_CONFIG_DIR", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
