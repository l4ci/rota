package worker

import (
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/host"
)

// HostConfig is the round's host settings, parsed once at the edge.
type HostConfig struct {
	// Dispatch is round.dispatch, "" when unset.
	Dispatch host.Kind
	// GroupSlots is round.herdrGroupSlots; GroupErr is a non-boolean value.
	GroupSlots bool
	GroupErr   error
}

// ParseHostConfig reads the host keys from cfg; the one place that knows them.
func ParseHostConfig(cfg any) HostConfig {
	c := HostConfig{Dispatch: host.KindOf(config.Dispatch(cfg))}
	c.GroupSlots, c.GroupErr = config.Bool(cfg, config.RoundHerdrGroupSlotsKey)
	return c
}

// PortConfig is the port range a round hands out: round.portBase and round.portBlock.
type PortConfig struct{ Base, Block int }

// ParsePortConfig reads the port keys from cfg. A value outside 1..65535
// reads as the default.
func ParsePortConfig(cfg any) PortConfig {
	c := PortConfig{Base: DefaultPortBase, Block: DefaultPortBlock}
	if n, err := config.Int(cfg, config.RoundPortBaseKey, 1, 65535); err == nil && n > 0 {
		c.Base = n
	}
	if n, err := config.Int(cfg, config.RoundPortBlockKey, 1, 65535); err == nil && n > 0 {
		c.Block = n
	}
	return c
}

// DepsConfig is what a slot's dependencies are prepared from: round.envSetup
// and round.sharedDirs.
type DepsConfig struct {
	EnvSetup   string
	SharedDirs []string
}

// ParseDepsConfig reads the dependency keys from cfg.
func ParseDepsConfig(cfg any) DepsConfig {
	return DepsConfig{
		EnvSetup:   strings.TrimSpace(config.String(cfg, config.RoundEnvSetupKey)),
		SharedDirs: config.SharedDirs(cfg),
	}
}
