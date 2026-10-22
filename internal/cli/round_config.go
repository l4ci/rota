package cli

import (
	"strings"

	"github.com/l4ci/rota/internal/roundcfg"
)

// roundConfigPolicy names the exceptions promised by the verb contracts.
// All other strict callers report an invalid config as a resolution error.
type roundConfigPolicy int

const (
	roundConfigRequired roundConfigPolicy = iota
	roundConfigUsage
	roundConfigBounce
)

// roundSettings is the CLI's round config load and error-to-exit mapping.
// Keep the parser's message intact: it already names the invalid key.
func (d *Deps) roundSettings(root string, policy roundConfigPolicy) (roundcfg.Settings, error) {
	set, err := roundcfg.FromConfig(d.Config(root))
	if err == nil {
		return set, nil
	}
	exit := ExitResolution
	switch policy {
	case roundConfigUsage:
		exit = ExitUsage
	case roundConfigBounce:
		// Only round.maxBounces has a documented internal-error exit for bounce.
		if strings.HasPrefix(err.Error(), "round.maxBounces ") {
			exit = ExitInternal
		}
	}
	return roundcfg.Settings{}, &Error{Exit: exit, Message: err.Error()}
}

// tolerantRoundSettings explicitly opts an advisory caller out of refusing on
// bad config. No partially parsed settings may escape a failed load.
func (d *Deps) tolerantRoundSettings(root string) (roundcfg.Settings, bool) {
	set, err := d.roundSettings(root, roundConfigRequired)
	return set, err == nil
}
