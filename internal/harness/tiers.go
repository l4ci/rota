package harness

import (
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/config"
)

// Tiers, light to heavy: the strengths a tier maps a model for, per harness kind.
const (
	TierLight    = "light"
	TierStandard = "standard"
	TierHeavy    = "heavy"
)

// Tiers lists the tiers in order.
var Tiers = []string{TierLight, TierStandard, TierHeavy}

// ProfileInherit is the round.profile value that names no model for any tier.
const ProfileInherit = "inherit"

// TierKey is the config key of one kind's model for one tier. Every reader of
// round.tiers builds its key here.
func TierKey(kind, tier string) string { return config.RoundTiersPrefix + kind + "." + tier }

// ParseTierKey is the inverse of TierKey: the kind and tier of a
// round.tiers.<kind>.<tier> key, !ok for any other key or an unknown tier.
func ParseTierKey(key string) (kind, tier string, ok bool) {
	rest, found := strings.CutPrefix(key, config.RoundTiersPrefix)
	if !found {
		return "", "", false
	}
	kind, tier, found = strings.Cut(rest, ".")
	if !found || kind == "" || !slices.Contains(Tiers, tier) {
		return "", "", false
	}
	return kind, tier, true
}

// TierMapSet is whether any tier of kind's round.tiers map names a model.
func TierMapSet(cfg any, kind string) bool {
	for _, tier := range Tiers {
		if strings.TrimSpace(config.StringIfSet(cfg, TierKey(kind, tier))) != "" {
			return true
		}
	}
	return false
}

// ProjectKind is the project's default worker harness: the first entry of
// round.harnesses, "" when the list is unset or empty.
func ProjectKind(cfg any) string {
	kinds, _ := RoundHarnesses(cfg)
	if len(kinds) == 0 {
		return ""
	}
	return kinds[0]
}
