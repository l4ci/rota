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
	"github.com/l4ci/rota/internal/labels"
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
	// Harnesses is round.harnesses: the kinds a round may use, in quota-fallback
	// order; the first is the project's default worker harness. Empty means the
	// slot's recorded kind, else claude, decides.
	Harnesses []string
	// FullOnly is true when test.fast is empty but test.full is set: workers
	// have no fast tier to prove against, so their brief says to run test.full.
	FullOnly bool
	// TDD is work.tdd: the brief repeats the red-first rule.
	TDD bool
	// StallMinutes is round.stallMinutes: how long a slot may make no progress
	// before `round reconcile` calls it stalled; 0 turns the check off.
	StallMinutes           int
	ClaimStaleMinutes      int
	ClaimStaleMinutesMajor int
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
	TrainFill    TrainFill
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
	// LegacySupportLabel preserves retired support offers until the label migration.
	LegacySupportLabel string
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

// ValidName reports whether n is usable as an agent or support machine name
// (agentRe).
func ValidName(n string) bool { return agentRe.MatchString(n) }

// ValidScope reports whether s is a scope.
func ValidScope(s string) bool {
	for _, v := range Scopes {
		if s == v {
			return true
		}
	}
	return false
}

// FromConfig reads and validates the round.* keys from an already loaded
// config, for a caller that holds one.
func FromConfig(cfg any) (Settings, error) {
	var s Settings
	if err := checkEnum(cfg, config.RoundScopeKey); err != nil {
		return s, err
	}
	s.Scope = config.String(cfg, config.RoundScopeKey)
	var err error
	if s.Roster, err = list(cfg, config.RoundRosterKey); err != nil {
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
	if s.Slots, err = config.Int(cfg, config.RoundSlotsKey, 1, config.MaxInt); err != nil {
		return s, err
	}
	s.Brief = config.String(cfg, config.RoundBriefKey)
	if s.SharedPaths, err = sharedPaths(cfg); err != nil {
		return s, err
	}
	if err := checkEnum(cfg, config.RoundScopeOverlapKey); err != nil {
		return s, err
	}
	s.BlockScopeOverlap = config.String(cfg, config.RoundScopeOverlapKey) == config.ScopeOverlapBlock
	if s.StallMinutes, err = config.Int(cfg, config.RoundStallMinutesKey, 0, config.MaxInt); err != nil {
		return s, err
	}
	if s.ClaimStaleMinutes, err = config.Int(cfg, config.RoundClaimStaleMinutesKey, 0, config.MaxInt); err != nil {
		return s, err
	}
	if s.ClaimStaleMinutesMajor, err = config.Int(cfg, config.RoundClaimStaleMinutesMajorKey, 0, config.MaxInt); err != nil {
		return s, err
	}
	if s.MaxBounces, err = config.Int(cfg, config.RoundMaxBouncesKey, 0, config.MaxInt); err != nil {
		return s, err
	}
	if s.FreshAt, err = config.Int(cfg, config.RoundFreshAtKey, 0, config.MaxInt); err != nil {
		return s, err
	}
	if s.LedgerKeep, err = config.Int(cfg, config.RoundLedgerKeepKey, 0, config.MaxInt); err != nil {
		return s, err
	}
	if s.ItemTimeoutMinutes, err = config.Int(cfg, config.RoundItemTimeoutMinutesKey, 0, config.MaxInt); err != nil {
		return s, err
	}
	if s.ArchitectureEvery, err = config.Int(cfg, config.RoundArchitectureEveryKey, 0, config.MaxInt); err != nil {
		return s, err
	}
	if s.ArchitectureAreas, err = list(cfg, config.RoundArchitectureAreasKey); err != nil {
		return s, err
	}
	if s.ArchitectureIdleMinItems, err = config.Int(cfg, config.RoundArchitectureIdleMinItemsKey, 0, config.MaxInt); err != nil {
		return s, err
	}
	if s.ArchitectureIdleMinHours, err = config.Int(cfg, config.RoundArchitectureIdleMinHoursKey, 0, config.MaxInt); err != nil {
		return s, err
	}
	s.Autopilot = config.String(cfg, "autonomy.level") == "auto"
	if s.AutopilotCap, err = config.Int(cfg, config.RoundAutopilotCapKey, 0, config.MaxInt); err != nil {
		return s, err
	}
	if s.TrainFill.Max, err = config.Int(cfg, config.RoundTrainMaxKey, 0, config.MaxInt); err != nil {
		return s, err
	}
	if s.TrainFill.Minutes, err = config.Int(cfg, config.RoundTrainFillMinutesKey, 0, config.MaxInt); err != nil {
		return s, err
	}
	if s.Batch, err = config.Int(cfg, config.RoundBatchKey, 0, config.MaxInt); err != nil {
		return s, err
	}
	if s.BundleMax, err = config.Int(cfg, config.RoundBundleMaxKey, 0, config.MaxInt); err != nil {
		return s, err
	}
	if err := checkEnum(cfg, config.RoundReviewLoopKey); err != nil {
		return s, err
	}
	s.ReviewLoop = config.String(cfg, config.RoundReviewLoopKey)
	s.AdoptPattern = strings.TrimSpace(config.String(cfg, config.RoundAdoptPatternKey))
	if _, err := path.Match(s.AdoptPattern, ""); err != nil {
		return s, fmt.Errorf("round.adoptPattern %q: %v", s.AdoptPattern, err)
	}
	s.Support = SupportName(cfg)
	s.LegacySupportLabel = LegacySupportLabel(cfg)
	if s.Role, err = MachineRole(cfg); err != nil {
		return s, err
	}
	if err := s.supportRoster(); err != nil {
		return s, err
	}
	if s.Harnesses, err = loadHarnesses(cfg); err != nil {
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

// CrossReview is what the cross-harness review requirement reads (#1612):
// review.crossLabels, the labels that trigger it; round.harnesses, the
// reviewers to pick from; and DefaultKind, the author's harness when no slot
// or harness: label names one.
type CrossReview struct {
	Labels, Harnesses []string
	DefaultKind       string
}

// ParseCrossReview reads the cross-harness review settings. A malformed
// review.crossLabels or round.harnesses is an error: the
// gate must not guess that a Major PR needs no cross review.
func ParseCrossReview(cfg any) (CrossReview, error) {
	var c CrossReview
	var err error
	if c.Labels, err = list(cfg, "review.crossLabels"); err != nil {
		return CrossReview{}, err
	}
	s := Settings{}
	if s.Harnesses, err = loadHarnesses(cfg); err != nil {
		return CrossReview{}, err
	}
	c.Harnesses, c.DefaultKind = s.Harnesses, s.DefaultKind()
	return c, nil
}

// Active reports whether any PR can need a cross-harness review: a trigger
// label and a harness list to pick the reviewer from are both set.
func (c CrossReview) Active() bool { return len(c.Labels) > 0 && len(c.Harnesses) > 0 }

// DefaultKind is the harness an issue with no pick resolves to:
// the first of round.harnesses, else ""; the slot's kind and claude decide.
func (s Settings) DefaultKind() string {
	if len(s.Harnesses) > 0 {
		return s.Harnesses[0]
	}
	return ""
}

// loadHarnesses reads round.harnesses: harness kinds in fallback order, none
// twice. A bad entry is an error, never skipped.
func loadHarnesses(cfg any) ([]string, error) {
	kinds, err := harness.RoundHarnesses(cfg)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, k := range kinds {
		if !harness.Valid(k) {
			return nil, fmt.Errorf("round.harnesses must list kinds from %s (got %q)", harness.KindList(), k)
		}
		if seen[k] {
			return nil, fmt.Errorf("round.harnesses lists %s twice", k)
		}
		seen[k] = true
	}
	return kinds, nil
}

// loadTiers reads round.tier and round.tiers.<kind>.<tier>. The claude standard
// tier defaults to sonnet (round.tiers.claude.standard).
// claude must be configured; a configured kind must name all three tiers.
func loadTiers(cfg any, s *Settings) error {
	v, err := config.Value(cfg, config.RoundTierKey)
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
	paths, _ := sharedPaths(cfg)
	return paths
}

// sharedPaths is the one reader of round.sharedPaths: Load reports its error,
// SharedPaths drops it.
func sharedPaths(cfg any) ([]string, error) { return list(cfg, config.RoundSharedPathsKey) }

// Labels are the issues.labels roles a round and the gate act on, with the
// schema defaults config.Label supplies.
type Labels struct {
	InProgress, NeedsHuman, NeedsReview string
}

// LabelsOf reads issue labels for round and gate consumers from a loaded config.
// Other consumers, such as backlog operations, may read these roles directly.
func LabelsOf(cfg any) Labels {
	return Labels{
		InProgress:  config.Label(cfg, "inProgress"),
		NeedsHuman:  config.Label(cfg, "needsHuman"),
		NeedsReview: config.Label(cfg, "needsReview"),
	}
}

// Sources of an effective tier model.
const (
	SourceExplicit       = "explicit"
	SourceProfile        = "profile"
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
	p := harness.RoundProfile(cfg)
	if _, ok := Profiles[p]; p != "" && !ok {
		return "", fmt.Errorf("round.profile must be %s, or unset (got %q)", strings.Join(ProfileNames, ", "), p)
	}
	return p, nil
}

// Effective is the model a kind's tier launches with, and where it came from:
// explicit (the tier key is set), profile (Claude only), default (the key's schema default) or harness default
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

// TierSet is the tier-set decision for one kind: whether its round.tiers map
// names a model, else the "tier=model" pairs a worker starts with.
func TierSet(cfg any, kind string) (set bool, models []string) {
	if harness.TierMapSet(cfg, kind) {
		return true, nil
	}
	for _, tier := range harness.Tiers {
		if m, _ := Effective(cfg, kind, tier); m != "" {
			models = append(models, tier+"="+m)
		}
	}
	return false, models
}

// SupportName is round.support read alone, trimmed: "" when unset or not a
// string. The bounce store reads it to pick the registry or the issue.
func SupportName(cfg any) string {
	return strings.TrimSpace(config.String(cfg, config.RoundSupportKey))
}

// LegacySupportLabel reads the retired key while upgrades still need its value.
// Repos that used the default never wrote the key.
func LegacySupportLabel(cfg any) string {
	if v, ok := config.Lookup(cfg, "issues.labels.support"); ok {
		if label, ok := v.(string); ok && strings.TrimSpace(label) != "" {
			return label
		}
	}
	return labels.LegacySupport
}

// MachineRole is round.role read alone, so a bad value in another round.* key cannot
// hide it: the support fence reads it before anything else.
func MachineRole(cfg any) (string, error) {
	v, err := config.Value(cfg, config.RoundRoleKey)
	if err != nil {
		return "", err
	}
	if r, _ := v.(string); r == RoleMain || r == RoleSupport {
		return r, nil
	}
	return "", fmt.Errorf("round.role must be %s or %s, not %v", RoleMain, RoleSupport, v)
}

// CheckMachineName validates the derived names shared by config and round entry.
// A main-only repo may leave support empty; a support role must have its own name.
func CheckMachineName(support, role string) error {
	if role == RoleSupport && support == "" {
		return fmt.Errorf("round.role is support but round.support is empty: set round.support to this machine's name")
	}
	if support == "" {
		return nil
	}
	if support == RoleMain {
		return fmt.Errorf("round.support: main is reserved for the main machine")
	}
	if !agentRe.MatchString(support) {
		return fmt.Errorf("round.support: %q is not a lowercase name usable in a branch (letters, digits, -)", support)
	}
	return nil
}

// supportRoster applies round.support to the roster (#1598): the support
// machine provisions every name as `<support>-<name>`, so its slots, branches,
// claims and workspaces never match main's, and main refuses a name that
// starts with that prefix. Off when round.support is empty.
func (s *Settings) supportRoster() error {
	if err := CheckMachineName(s.Support, s.Role); err != nil {
		return err
	}
	if s.Support == "" {
		return nil
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

// MachinePrefix is the fixed prefix of labels pinning issues to a machine.
const MachinePrefix = labels.MachinePrefix

// MachineLabel is the pin for name, or empty when support is disabled.
func MachineLabel(name string) string {
	if name == "" {
		return ""
	}
	return MachinePrefix + name
}

// MachineName derives the local name without a second config key.
func MachineName(support, role string) string {
	if role == RoleSupport {
		return support
	}
	return RoleMain
}

// MachineName is this machine's derived name.
func (s Settings) MachineName() string { return MachineName(s.Support, s.Role) }

// PinsOf returns nonempty machine names, preserving label order.
func PinsOf(labels []string) []string {
	var pins []string
	for _, label := range labels {
		if name, ok := strings.CutPrefix(label, MachinePrefix); ok && name != "" {
			pins = append(pins, name)
		}
	}
	return pins
}
