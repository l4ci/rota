// Package roundcfg reads the round.* config keys (C3, #59).
package roundcfg

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/rotatree"
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

// Values of round.reviewLoop.
const (
	ReviewLoopManual = "manual"
	ReviewLoopAuto   = "auto"
)

// Tiers, light to heavy: the strengths a tier maps a model for, per harness kind.
const (
	TierLight    = "light"
	TierStandard = "standard"
	TierHeavy    = "heavy"
)

// Tiers lists the tiers in order.
var Tiers = []string{TierLight, TierStandard, TierHeavy}

// Roles are the built-in agent roles, each with the default tier it runs on.
// `rota agents write` emits one subagent definition per role.
var Roles = []string{"explorer", "implementer", "reasoner"}

// ClaudeEfforts are the effort values Claude Code accepts for a subagent.
var ClaudeEfforts = []string{"low", "medium", "high", "xhigh", "max"}

// Role is one agent role's settings: the tier its model comes from and the
// optional effort ("" leaves the harness default).
type Role struct{ Tier, Effort string }

// Settings are the round.* keys.
type Settings struct {
	Scope       string
	Roster      []string
	Brief       string
	SharedPaths []string
	// ScopeOverlap is round.scopeOverlap: "warn" (default) or "block". Block
	// makes a clash on declared scopes fail the overlap check like a shared path.
	ScopeOverlap string
	// Tier is the default tier; Models maps kind -> tier -> model name for
	// every configured kind (a kind with no tier set is absent).
	Tier   string
	Models map[string]map[string]string
	// WorkerKind is round.workerKind: the project's default worker harness,
	// "" when unset (the slot's recorded kind, else claude, decides).
	WorkerKind string
	// StallMinutes is round.stallMinutes: how long a slot may make no progress
	// before `round reconcile` calls it stalled; 0 turns the check off.
	StallMinutes int
	// MaxBounces is round.maxBounces: how often `rota worker gate` may send one
	// item's PR back to its worker before parking the item as needs-human; 0
	// turns the cap off.
	MaxBounces int
	// ItemTimeoutMinutes is work.itemTimeoutMinutes: how long one item may run
	// from its first assignment before `round reconcile` reports it
	// item-timeout; 0 turns the cap off.
	ItemTimeoutMinutes int
	// ArchitectureEvery is round.architectureEvery: closed non-refactor items
	// between architecture reviews; 0 turns the automatic review off.
	// ArchitectureAreas is round.architectureAreas: the areas a review is
	// split into; empty means the subsystem map, else one whole-repo review.
	ArchitectureEvery int
	ArchitectureAreas []string
	// Autopilot is round.autopilot: `rota round watch --autopilot` and `rota
	// round tick` do the mechanical steps. AutopilotCap is round.autopilotCap,
	// the most assigns and the most merges one tick does.
	Autopilot    bool
	AutopilotCap int
	// ReviewLoop is round.reviewLoop: "manual" or "auto" (ReviewLoopManual,
	// ReviewLoopAuto). Under auto the forge poll relays a done slot's review
	// input to its worker itself.
	ReviewLoop string
	// Roles is roles.<role>.tier and .effort for every name in Roles.
	Roles map[string]Role
}

// ValidTier reports whether s is a tier.
func ValidTier(s string) bool { return indexOf(Tiers, s) >= 0 }

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
	cfg := config.Load(rotatree.Config(root))
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
	v, err = config.Value(cfg, "round.scopeOverlap")
	if err != nil {
		return s, err
	}
	s.ScopeOverlap, _ = v.(string)
	if s.ScopeOverlap != "warn" && s.ScopeOverlap != "block" {
		return s, fmt.Errorf("round.scopeOverlap must be warn or block (got %v)", v)
	}
	if s.StallMinutes, err = config.Int(cfg, "round.stallMinutes", 0, config.MaxInt); err != nil {
		return s, err
	}
	if s.MaxBounces, err = config.Int(cfg, "round.maxBounces", 0, config.MaxInt); err != nil {
		return s, err
	}
	if s.ItemTimeoutMinutes, err = config.Int(cfg, "work.itemTimeoutMinutes", 0, config.MaxInt); err != nil {
		return s, err
	}
	if s.ArchitectureEvery, err = config.Int(cfg, "round.architectureEvery", 0, config.MaxInt); err != nil {
		return s, err
	}
	if s.ArchitectureAreas, err = list(cfg, "round.architectureAreas"); err != nil {
		return s, err
	}
	v, err = config.Value(cfg, "round.autopilot")
	if err != nil {
		return s, err
	}
	b, ok := v.(bool)
	if !ok {
		return s, fmt.Errorf("round.autopilot must be true or false (got %v)", v)
	}
	s.Autopilot = b
	if s.AutopilotCap, err = config.Int(cfg, "round.autopilotCap", 0, config.MaxInt); err != nil {
		return s, err
	}
	v, err = config.Value(cfg, "round.reviewLoop")
	if err != nil {
		return s, err
	}
	if s.ReviewLoop, _ = v.(string); s.ReviewLoop != ReviewLoopManual && s.ReviewLoop != ReviewLoopAuto {
		return s, fmt.Errorf("round.reviewLoop must be %s or %s (got %v)", ReviewLoopManual, ReviewLoopAuto, v)
	}
	if s.WorkerKind, err = loadWorkerKind(cfg); err != nil {
		return s, err
	}
	if err := loadTiers(cfg, &s); err != nil {
		return s, err
	}
	return s, loadRoles(cfg, &s)
}

// loadRoles reads roles.<role>.tier and .effort. The effort is checked against
// Claude's values here; Codex's narrower set is checked when its files are
// written, since it only matters then.
func loadRoles(cfg any, s *Settings) error {
	s.Roles = map[string]Role{}
	for _, name := range Roles {
		var r Role
		for _, f := range []string{"tier", "effort"} {
			key := "roles." + name + "." + f
			v, err := config.Value(cfg, key)
			if err != nil {
				return err
			}
			str, ok := v.(string)
			if !ok {
				return fmt.Errorf("%s must be a string (got %v)", key, v)
			}
			if f == "tier" {
				r.Tier = str
			} else {
				r.Effort = str
			}
		}
		if !ValidTier(r.Tier) {
			return fmt.Errorf("roles.%s.tier must be %s (got %q)", name, strings.Join(Tiers, ", "), r.Tier)
		}
		if r.Effort != "" && indexOf(ClaudeEfforts, r.Effort) < 0 {
			return fmt.Errorf("roles.%s.effort must be %s, or empty (got %q)", name, strings.Join(ClaudeEfforts, ", "), r.Effort)
		}
		s.Roles[name] = r
	}
	return nil
}

// loadWorkerKind reads round.workerKind: a harness kind, or "" for unset. A
// bad value is an error, never a silent fallback to claude.
func loadWorkerKind(cfg any) (string, error) {
	v, err := config.Value(cfg, "round.workerKind")
	if err != nil {
		return "", err
	}
	k, ok := v.(string)
	if !ok || (k != "" && !harness.Valid(k)) {
		return "", fmt.Errorf("round.workerKind must be %s (got %v)", harness.KindList(), v)
	}
	return k, nil
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
	for _, kind := range harness.Kinds {
		m := map[string]string{}
		for _, tier := range Tiers {
			key := "round.tiers." + kind + "." + tier
			v, err := config.Value(cfg, key)
			if err != nil {
				return err
			}
			name, _ := v.(string)
			name = strings.TrimSpace(name)
			if name == "" && kind == harness.Claude && tier == TierStandard {
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
		case set == 0 && kind == harness.Claude:
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

// SharedPaths reads round.sharedPaths from a loaded config: the globs two
// branches may both touch without counting as an overlap. An unreadable value
// yields none, so a caller that only filters (the merge gate) is not blocked by
// a config error that Load reports elsewhere.
func SharedPaths(cfg any) []string {
	paths, _ := list(cfg, "round.sharedPaths")
	return paths
}
