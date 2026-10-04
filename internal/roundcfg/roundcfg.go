// Package roundcfg reads the round.* config keys (C3, #59).
package roundcfg

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/l4ci/rota/internal/config"
)

// Scope values of round.scope: which issues a round may take.
const (
	ScopeSlate     = "slate"
	ScopeMilestone = "milestone"
	ScopeNext      = "next"
	ScopeOpen      = "open"
)

// Scopes lists the valid scope values.
var Scopes = []string{ScopeSlate, ScopeMilestone, ScopeNext, ScopeOpen}

// Tiers, light to heavy, and the harness kinds a tier maps a model for.
const (
	TierLight    = "light"
	TierStandard = "standard"
	TierHeavy    = "heavy"

	KindClaude = "claude"
	KindCodex  = "codex"
)

// Tiers lists the tiers in order; Kinds the harness kinds.
var (
	Tiers = []string{TierLight, TierStandard, TierHeavy}
	Kinds = []string{KindClaude, KindCodex}
)

// Settings are the round.* keys.
type Settings struct {
	Scope       string
	Roster      []string
	Brief       string
	SharedPaths []string
	// Tier is the default tier; Models maps kind -> tier -> model name for
	// every configured kind (a kind with no tier set is absent).
	Tier   string
	Models map[string]map[string]string
	// StallMinutes is round.stallMinutes: how long a slot may make no progress
	// before `round reconcile` calls it stalled; 0 turns the check off.
	StallMinutes int
}

// ValidTier reports whether s is a tier; ValidKind whether s is a harness kind.
func ValidTier(s string) bool { return indexOf(Tiers, s) >= 0 }
func ValidKind(s string) bool { return indexOf(Kinds, s) >= 0 }

// TierRank orders tiers: light 0, standard 1, heavy 2; -1 for a non-tier.
func TierRank(s string) int { return indexOf(Tiers, s) }

func indexOf(l []string, s string) int {
	for i, v := range l {
		if v == s {
			return i
		}
	}
	return -1
}

// Model is the model name for a kind and tier, "" when the kind has no map.
func (s Settings) Model(kind, tier string) string { return s.Models[kind][tier] }

// agentRe is a name usable in a branch (`<agent>/<issue>-<slug>`,
// `park/<agent>`) and a directory (`.worktrees/<agent>`).
var agentRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// ValidScope reports whether s is a scope.
func ValidScope(s string) bool {
	for _, v := range Scopes {
		if s == v {
			return true
		}
	}
	return false
}

// Load reads and validates the round.* keys from the project config.
func Load(root string) (Settings, error) {
	cfg := config.Load(filepath.Join(root, ".rota", "config.json"))
	var s Settings
	v, err := config.Value(cfg, "round.scope")
	if err != nil {
		return s, err
	}
	s.Scope, _ = v.(string)
	if !ValidScope(s.Scope) {
		return s, fmt.Errorf("round.scope must be %s (got %v)", strings.Join(Scopes, ", "), v)
	}
	if s.Roster, err = list(cfg, "round.roster"); err != nil {
		return s, err
	}
	seen := map[string]bool{}
	for _, n := range s.Roster {
		if !agentRe.MatchString(n) {
			return s, fmt.Errorf("round.roster: %q is not a lowercase name usable in a branch (letters, digits, -)", n)
		}
		if seen[n] {
			return s, fmt.Errorf("round.roster: %q is listed twice", n)
		}
		seen[n] = true
	}
	if len(s.Roster) == 0 {
		return s, fmt.Errorf("round.roster is empty")
	}
	v, err = config.Value(cfg, "round.brief")
	if err != nil {
		return s, err
	}
	s.Brief, _ = v.(string)
	if s.SharedPaths, err = list(cfg, "round.sharedPaths"); err != nil {
		return s, err
	}
	v, err = config.Value(cfg, "round.stallMinutes")
	if err != nil {
		return s, err
	}
	n, ok := v.(interface{ Int64() (int64, error) })
	if !ok {
		return s, fmt.Errorf("round.stallMinutes must be a non-negative integer (got %v)", v)
	}
	i, err := n.Int64()
	if err != nil || i < 0 {
		return s, fmt.Errorf("round.stallMinutes must be a non-negative integer (got %v)", v)
	}
	s.StallMinutes = int(i)
	return s, loadTiers(cfg, &s)
}

// loadTiers reads round.tier and round.tiers.<kind>.<tier>. The claude standard
// tier falls back to models.worker, so one knob governs the standard model.
// claude must be configured; a configured kind must name all three tiers.
func loadTiers(cfg any, s *Settings) error {
	v, err := config.Value(cfg, "round.tier")
	if err != nil {
		return err
	}
	s.Tier, _ = v.(string)
	if !ValidTier(s.Tier) {
		return fmt.Errorf("round.tier must be %s (got %v)", strings.Join(Tiers, ", "), v)
	}
	s.Models = map[string]map[string]string{}
	for _, kind := range Kinds {
		m := map[string]string{}
		for _, tier := range Tiers {
			key := "round.tiers." + kind + "." + tier
			v, err := config.Value(cfg, key)
			if err != nil {
				return err
			}
			name, _ := v.(string)
			name = strings.TrimSpace(name)
			if name == "" && kind == KindClaude && tier == TierStandard {
				w, err := config.Value(cfg, "models.worker")
				if err != nil {
					return err
				}
				name, _ = w.(string)
				name = strings.TrimSpace(name)
			}
			m[tier] = name
		}
		set := 0
		for _, n := range m {
			if n != "" {
				set++
			}
		}
		switch {
		case set == 0 && kind == KindClaude:
			return fmt.Errorf("round.tiers.claude is not configured: set light, standard and heavy")
		case set == 0:
			continue
		case set < len(Tiers):
			for _, tier := range Tiers {
				if m[tier] == "" {
					return fmt.Errorf("round.tiers.%s.%s is empty: a configured kind needs a model for every tier", kind, tier)
				}
			}
		}
		s.Models[kind] = m
	}
	return nil
}

func list(cfg any, key string) ([]string, error) {
	v, err := config.Value(cfg, key)
	if err != nil {
		return nil, err
	}
	raw, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a list of strings", key)
	}
	var out []string
	for _, e := range raw {
		str, ok := e.(string)
		if !ok {
			return nil, fmt.Errorf("%s must be a list of strings", key)
		}
		if str = strings.TrimSpace(str); str != "" {
			out = append(out, str)
		}
	}
	return out, nil
}
