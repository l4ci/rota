package worker

import (
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/rotatree"
)

// Tests write .rota/config.json and expect an Env with no Config to read it.
func init() {
	defaultConfig = func(root string) any { return config.Load(rotatree.Config(root)) }
}
