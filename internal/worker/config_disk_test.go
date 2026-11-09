package worker

import (
	"github.com/l4ci/rota/internal/cfgdir"
	"github.com/l4ci/rota/internal/config"
)

// Tests write .rota/config.json and expect an Env with no Config to read it.
func init() {
	defaultConfig = func(root string) any { return config.Load(cfgdir.OSEnv(), root) }
}
