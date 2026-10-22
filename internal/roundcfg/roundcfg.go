// Package roundcfg reads the round.* config keys (C3, #59).
package roundcfg

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/rotatree"
)

// Scope values of round.scope: which issues a round may take.
const (
	ScopeSlate     = config.ScopeSlate
	ScopeMilestone = config.ScopeMilestone
	ScopeNext      = config.ScopeNext
	ScopeOpen      = config.ScopeOpen
)

// Scopes lists the valid scope values.
var Scopes = []string{ScopeSlate, ScopeMilestone, ScopeNext, ScopeOpen}

// Values of round.reviewLoop.
const (
	ReviewLoopManual = "manual"
	ReviewLoopAuto   = "auto"
)

// Values of round.role.
const (
	RoleMain    = "main"
	RoleSupport = "support"
)

// The tiers are owned by harness, which roundcfg already imports.
const (
	TierLight    = harness.TierLight
	TierStandard = harness.TierStandard
	TierHeavy    = harness.TierHeavy
)

// Tiers lists the tiers in order.
var Tiers = harness.Tiers

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
	Scope  string
	Roster []string
	// Slots is round.slots: how many roster names, from the first, a round
	// started without --slots uses.
	Slots       int
	Brief       string
	SharedPaths []string
	// BlockScopeOverlap is round.scopeOverlap == block: a clash on declared
	// scopes fails the overlap check like a shared path. Warn (default) only reports.
	BlockScopeOverlap bool
	// Tier is the default tier; Models maps kind -> tier -> model name for
	// every kind with a model (shipped defaults count; a kind whose tiers are
	// all set empty is absent). Tiered is the kinds the project opted into:
	// claude, and a kind whose tier map it set itself.
	Tier string
	// Profile is round.profile: "" or a name in Profiles; it fills the Claude tiers.
	Profile string
	Models  map[string]map[string]string
	Tiered  map[string]bool
	// WorkerKind is round.workerKind: the project's default worker harness,
	// "" when unset (the slot's recorded kind, else claude, decides).
	WorkerKind string
	// FullOnly is true when test.fast is empty but test.full is set: workers
	// have no fast tier to prove against, so their brief says to run test.full.
	FullOnly bool
	// TDD is work.tdd: the brief repeats the red-first rule.
	TDD bool
	// StallMinutes is round.stallMinutes: how long a slot may make no progress
	// before `round reconcile` calls it stalled; 0 turns the check off.
	StallMinutes int
	// MaxBounces is round.maxBounces: how often `rota worker gate` may send one
	// item's PR back to its worker before parking the item as needs-human; 0
	// turns the cap off.
	MaxBounces int
	// FreshAt is round.freshAt: the bounce at which `round review-relay`
	// transfers the branch to a fresh slot instead of resuming the worker; 0
	// turns it off.
	FreshAt int
	// LedgerKeep is round.ledgerKeep: how many rounds of the round ledger a
	// new round keeps; 0 keeps everything.
	LedgerKeep int
	// ItemTimeoutMinutes is round.itemTimeoutMinutes: how long one item may run
	// from its first assignment before `round reconcile` reports it
	// item-timeout; 0 turns the cap off.
	ItemTimeoutMinutes int
	// ArchitectureEvery is round.architectureEvery: closed non-refactor items
	// between architecture reviews; 0 turns the automatic review off.
	// ArchitectureAreas is round.architectureAreas: the areas a review is
	// split into; empty means the subsystem map, else one whole-repo review.
	ArchitectureEvery int
	ArchitectureAreas []string
	// ArchitectureIdleMinItems and ArchitectureIdleMinHours are
	// round.architectureIdleMinItems and round.architectureIdleMinHours: the
	// closed items and the hours since the last review the queue-empty trigger
	// needs; 0 drops that condition.
	ArchitectureIdleMinItems int
	ArchitectureIdleMinHours int
	// Autopilot is autonomy.level auto (#1101; round.autopilot is retired):
	// `rota round watch --autopilot` and `rota round tick` do the mechanical
	// steps. AutopilotCap is round.autopilotCap,
	// the most assigns and the most merges one tick does.
	Autopilot    bool
	AutopilotCap int
	// Batch is round.batch: the most items a round has assigned and not yet
	// landed or parked at once; 0 means no limit.
	Batch int
	// BundleMax is round.bundleMax: the most member issues `round assign
	// --bundle` hands one slot beside the lead; 0 means no limit.
	BundleMax int
	// ReviewLoop is round.reviewLoop: "manual" or "auto" (ReviewLoopManual,
	// ReviewLoopAuto). Under auto the forge poll relays a done slot's review
	// input to its worker itself.
	ReviewLoop string
	// AdoptPattern is round.adoptPattern: a glob over branch names `round
	// reconcile` reports as unregistered-branch when no slot holds them; "" turns
	// the check off.
	AdoptPattern string
	// Support is round.support: the support machine's name and roster prefix;
	// "" leaves the support orchestrator off.
	Support string
	// Role is round.role: RoleMain or RoleSupport, per machine.
	Role string
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
	return FromConfig(config.Load(rotatree.Config(root)))
}

// FromConfig reads and validates the round.* keys from an already loaded
// config, for a caller that holds one.
func FromConfig(cfg any) (Settings, error) {
	var s Settings
	if err := checkEnum(cfg, "round.scope"); err != nil {
		return s, err
	}
	s.Scope = config.String(cfg, "round.scope")
	var err error
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
	if s.Slots, err = config.Int(cfg, "round.slots", 1, config.MaxInt); err != nil {
		return s, err
	}
	s.Brief = config.String(cfg, "round.brief")
	if s.SharedPaths, err = list(cfg, "round.sharedPaths"); err != nil {
		return s, err
	}
	if err := checkEnum(cfg, "round.scopeOverlap"); err != nil {
		return s, err
	}
	s.BlockScopeOverlap = config.String(cfg, "round.scopeOverlap") == config.ScopeOverlapBlock
	if s.StallMinutes, err = config.Int(cfg, "round.stallMinutes", 0, config.MaxInt); err != nil {
		return s, err
	}
	if s.MaxBounces, err = config.Int(cfg, "round.maxBounces", 0, config.MaxInt); err != nil {
		return s, err
	}
	if s.FreshAt, err = config.Int(cfg, "round.freshAt", 0, config.MaxInt); err != nil {
		return s, err
	}
	if s.LedgerKeep, err = config.Int(cfg, "round.ledgerKeep", 0, config.MaxInt); err != nil {
		return s, err
	}
	if s.ItemTimeoutMinutes, err = config.Int(cfg, "round.itemTimeoutMinutes", 0, config.MaxInt); err != nil {
		return s, err
	}
	if s.ArchitectureEvery, err = config.Int(cfg, "round.architectureEvery", 0, config.MaxInt); err != nil {
		return s, err
	}
	if s.ArchitectureAreas, err = list(cfg, "round.architectureAreas"); err != nil {
		return s, err
	}
	if s.ArchitectureIdleMinItems, err = config.Int(cfg, "round.architectureIdleMinItems", 0, config.MaxInt); err != nil {
		return s, err
	}
	if s.ArchitectureIdleMinHours, err = config.Int(cfg, "round.architectureIdleMinHours", 0, config.MaxInt); err != nil {
		return s, err
	}
	s.Autopilot = config.String(cfg, "autonomy.level") == "auto"
	if s.AutopilotCap, err = config.Int(cfg, "round.autopilotCap", 0, config.MaxInt); err != nil {
		return s, err
	}
	if s.Batch, err = config.Int(cfg, "round.batch", 0, config.MaxInt); err != nil {
		return s, err
	}
	if s.BundleMax, err = config.Int(cfg, "round.bundleMax", 0, config.MaxInt); err != nil {
		return s, err
	}
	if err := checkEnum(cfg, "round.reviewLoop"); err != nil {
		return s, err
	}
	s.ReviewLoop = config.String(cfg, "round.reviewLoop")
	s.AdoptPattern = strings.TrimSpace(config.String(cfg, "round.adoptPattern"))
	if _, err := path.Match(s.AdoptPattern, ""); err != nil {
		return s, fmt.Errorf("round.adoptPattern %q: %v", s.AdoptPattern, err)
	}
	s.Support = strings.TrimSpace(config.String(cfg, "round.support"))
	if s.Role, err = MachineRole(cfg); err != nil {
		return s, err
	}
	if err := s.supportRoster(); err != nil {
		return s, err
	}
	if s.WorkerKind, err = loadWorkerKind(cfg); err != nil {
		return s, err
	}
	if err := loadTiers(cfg, &s); err != nil {
		return s, err
	}
	t := ParseTests(cfg)
	if t.Err != nil {
		return s, t.Err
	}
	if t.TDDErr != nil {
		return s, t.TDDErr
	}
	s.FullOnly, s.TDD = t.FullOnly(), t.TDD
	return s, loadRoles(cfg, &s)
}

// checkEnum validates an enum key against its schema choices. It reads the raw
// value, so the error names a number, bool or null that config.String would
// collapse to "".
func checkEnum(cfg any, key string) error {
	v, err := config.Value(cfg, key)
	if err != nil {
		return err
	}
	return config.CheckEnum(key, v)
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
	if s.Profile, err = profileOf(cfg); err != nil {
		return err
	}
	s.Models = map[string]map[string]string{}
	s.Tiered = map[string]bool{}
	for _, kind := range harness.Kinds() {
		rule := harness.Resolve(kind).Tiers()
		s.Tiered[kind] = rule.Required || harness.TierMapSet(cfg, kind)
		m := map[string]string{}
		for _, tier := range Tiers {
			if _, err := config.Value(cfg, harness.TierKey(kind, tier)); err != nil {
				return err
			}
			m[tier], _ = Effective(cfg, kind, tier)
		}
		set := 0
		for _, n := range m {
			if n != "" {
				set++
			}
		}
		switch {
		case rule.Profile && s.Profile == ProfileInherit:
			// inherit: an empty tier is deliberate, its workers launch without --model.
		case set == 0 && rule.Required:
			return fmt.Errorf("round.tiers.%s is not configured: set light, standard and heavy", kind)
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
	return listValue(key, v)
}

// listValue is list on a value already read.
func listValue(key string, v any) ([]string, error) {
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

// Tests is the one typed parse of test.fast, test.full and work.tdd. The gate
// (worker.GateConfig) and the round settings both read it, so the two cannot
// disagree on the legacy key or on a malformed value.
type Tests struct {
	// Fast is test.fast. Full is test.full, or the deprecated
	// refactor.verifyCommands while a config holds only that (Legacy).
	Fast, Full []string
	Legacy     bool
	// TDD is work.tdd.
	TDD bool
	// Err is every malformed test.fast or test.full (joined); a malformed list
	// is nil. TDDErr is a non-boolean work.tdd. They are kept apart so the gate
	// can defer each to the step that needs it.
	Err, TDDErr error
}

// ParseTests reads the test commands and work.tdd from a loaded config.
func ParseTests(cfg any) Tests {
	var t Tests
	var errFast, errFull error
	t.Fast, errFast = list(cfg, "test.fast")
	t.Full, errFull = list(cfg, config.TestFullKey)
	if len(t.Full) == 0 && errFull == nil {
		// The legacy key is not in the schema, so it is looked up, not defaulted.
		if v, ok := config.Lookup(cfg, config.LegacyVerifyKey); ok {
			if old, err := listValue(config.LegacyVerifyKey, v); err == nil && len(old) > 0 {
				t.Full, t.Legacy = old, true
			}
		}
	}
	t.Err = errors.Join(errFast, errFull)
	t.TDD, t.TDDErr = config.Bool(cfg, "work.tdd")
	return t
}

// FullOnly is true when test.fast is empty but a full command is set.
func (t Tests) FullOnly() bool { return len(t.Fast) == 0 && len(t.Full) > 0 }

// SharedPaths reads round.sharedPaths from a loaded config: the globs two
// branches may both touch without counting as an overlap. An unreadable value
// yields none, so a caller that only filters (the merge gate) is not blocked by
// a config error that Load reports elsewhere.
func SharedPaths(cfg any) []string {
	paths, _ := list(cfg, "round.sharedPaths")
	return paths
}

// Sources of an effective tier model.
const (
	SourceExplicit       = "explicit"
	SourceProfile        = "profile"
	SourceModelsWorker   = "models.worker"
	SourceDefault        = "default"
	SourceHarnessDefault = "harness default"
)

// ProfileInherit is the round.profile value that names no model.
const ProfileInherit = harness.ProfileInherit

// Profiles maps each round.profile name to its Claude models, light to heavy.
// inherit names no model: workers launch without --model.
var Profiles = map[string][3]string{
	"quality":      {"sonnet", "opus", "opus"},
	"balanced":     {"haiku", "sonnet", "opus"},
	"budget":       {"haiku", "haiku", "sonnet"},
	ProfileInherit: {"", "", ""},
}

// ProfileNames lists the valid round.profile values.
var ProfileNames = []string{"quality", "balanced", "budget", ProfileInherit}

// profileOf reads round.profile: "" when unset, an error for an unknown name.
func profileOf(cfg any) (string, error) {
	p := strings.TrimSpace(config.String(cfg, "round.profile"))
	if _, ok := Profiles[p]; p != "" && !ok {
		return "", fmt.Errorf("round.profile must be %s, or unset (got %q)", strings.Join(ProfileNames, ", "), p)
	}
	return p, nil
}

// Effective is the model a kind's tier launches with, and where it came from:
// explicit (the tier key is set), profile (Claude only), models.worker (the
// standard fallback), default (the key's schema default) or harness default
// (no model, so the harness picks).
func Effective(cfg any, kind, tier string) (model, source string) {
	key := harness.TierKey(kind, tier)
	if v, ok := config.Lookup(cfg, key); ok {
		if s, _ := v.(string); strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s), SourceExplicit
		}
	}
	if harness.Resolve(kind).Tiers().Profile {
		if p, _ := profileOf(cfg); p != "" {
			if m := Profiles[p][indexOf(Tiers, tier)]; m != "" {
				return m, SourceProfile
			}
			return "", SourceHarnessDefault
		}
	}
	if fb := harness.Resolve(kind).Tiers().StandardFallback; fb != "" && tier == TierStandard {
		if m := strings.TrimSpace(config.String(cfg, fb)); m != "" {
			return m, SourceModelsWorker
		}
	}
	if _, set := config.Lookup(cfg, key); set {
		// Set but empty (and no profile, no fallback): the harness picks, and
		// loadTiers refuses it for a claude tier.
		return "", SourceHarnessDefault
	}
	if m := strings.TrimSpace(config.String(cfg, key)); m != "" {
		return m, SourceDefault
	}
	return "", SourceHarnessDefault
}

// SupportName is round.support read alone, trimmed: "" when unset or not a
// string. The bounce store reads it to pick the registry or the issue.
func SupportName(cfg any) string {
	return strings.TrimSpace(config.String(cfg, "round.support"))
}

// MachineRole is round.role read alone, so a bad value in another round.* key cannot
// hide it: the support fence reads it before anything else.
func MachineRole(cfg any) (string, error) {
	v, err := config.Value(cfg, "round.role")
	if err != nil {
		return "", err
	}
	if r, _ := v.(string); r == RoleMain || r == RoleSupport {
		return r, nil
	}
	return "", fmt.Errorf("round.role must be %s or %s, not %v", RoleMain, RoleSupport, v)
}

// supportRoster applies round.support to the roster (#1598): the support
// machine provisions every name as `<support>-<name>`, so its slots, branches,
// claims and workspaces never match main's, and main refuses a name that
// starts with that prefix. Off when round.support is empty.
func (s *Settings) supportRoster() error {
	if s.Support == "" {
		return nil
	}
	if !agentRe.MatchString(s.Support) {
		return fmt.Errorf("round.support: %q is not a lowercase name usable in a branch (letters, digits, -)", s.Support)
	}
	prefix := s.Support + "-"
	for i, n := range s.Roster {
		switch {
		case s.Role == RoleSupport:
			s.Roster[i] = prefix + n
		case strings.HasPrefix(n, prefix):
			return fmt.Errorf("round.roster: %q starts with %q, the support machine's prefix (round.support), so it could match a support slot", n, prefix)
		}
	}
	return nil
}
