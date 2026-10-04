package config

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/jsonx"
)

// Key is one known .rota/config.json key: its dotted name, the default used
// when the key is absent, and whether rota init writes it. Defaults use the
// jsonx value types: string, bool, json.Number and []any.
type Key struct {
	Name     string // dotted path, e.g. "work.mergeStrategy"
	Default  any    // value when the key is missing or null
	Required bool   // written by rota init; the schema check treats it as present-or-stale
}

// VersionKey is the stamp of the rota release that wrote a project's config.
// LegacyVersionKeys are where hv, before the rename to rota (#236), kept it.
// They are read as fallbacks and moved by Fill. hvSkills.version is only
// migrate hv's to move.
const VersionKey = "rota.version"

var LegacyVersionKeys = []string{"hv.version"}

// StampedVersion is the version stamped in cfg: the string at VersionKey,
// else the first one at a LegacyVersionKeys key, else "".
func StampedVersion(cfg any) string {
	for _, k := range append([]string{VersionKey}, LegacyVersionKeys...) {
		if v, ok := walk(cfg, k); ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// Keys is the table of every known config key. Leaf keys only: object-valued
// parents are not rows. The first PythonKeys rows are CONFIG_KEYS of the
// retired bin/hvlib_config.py, in its order, which the parity goldens freeze;
// keys added in 5.0 follow them.
var Keys = []Key{
	{"models.orchestrator", "opus", true},
	{"models.worker", "sonnet", true},
	{"work.isolation", "branch", true},
	{"work.mergeStrategy", "direct", true},
	{"work.dispatch", "subagent", true},
	{"work.workerSlots", json.Number("3"), true},
	{"work.workerCommand", "", true},
	{"work.accounts", []any{}, true},
	{"work.operatorCommand", "", true},
	{"refactor.confirmBeforeExecute", true, true},
	{"refactor.verifyCommands", []any{}, true},
	{"learn.verify", true, true},
	{"learn.promoteThreshold", json.Number("3"), true},
	{"ship.review", true, true},
	{"ship.secondOpinion", false, true},
	{"ship.secondOpinionRunner", "subagent", true},
	{"ship.qa", false, true},
	{"qa.gate", "advisory", true},
	{"qa.afterWork", false, true},
	{"autonomy.level", "off", true},
	{"debug.competingHypotheses", false, true},
	{"docs.path", "docs", true},
	{"docs.autoCreate", false, true},
	{"docs.afterWork", false, true},
	{"git.baseBranch", "", true},
	{"umbrella.enabled", false, true},
	{"issues.providers.github", true, true},
	{"issues.providers.gitlab", true, true},
	{VersionKey, "", true},
	{"loop.webResearch", false, false},
	{"issues.label", "in-progress", false},
	{"backlog.backend", "file", false},
	{"issues.provider", "auto", false},
	{"issues.retryWaitSeconds", json.Number("60"), false},
	{"issues.bulkPaceMs", json.Number("1000"), false},
	{"issues.labels.inProgress", "in-progress", false},
	{"issues.labels.needsReview", "needs-review", false},
	{"issues.labels.changesRequested", "changes-requested", false},
	{"issues.labels.released", "released", false},
	{"issues.labels.notPlanned", "not-planned", false},
	{"issues.labels.blocked", "blocked", false},
	{"issues.labels.milestoneTracker", "milestone-tracker", false},
	{"issues.labels.types.bug", "type:bug", false},
	{"issues.labels.types.feature", "type:feature", false},
	{"issues.labels.types.task", "type:task", false},
	{"issues.labels.priorityPrefix", "p", false},
	{"issues.labels.sizePrefix", "size:", false},
	{"issues.autoCreateLabel", true, false},
	{"issues.filterMineOnly", false, false},
	{"issues.homeRepo", "", false},
	{"release.checklistPath", ".rota/RELEASE.md", false},
	{"release.confirmLargePushCommits", json.Number("10"), false},
	{"release.nudgeAfterCommits", json.Number("10"), false},
	{"release.nudgeAfterDays", json.Number("14"), false},
	// 5.0 keys: not in CONFIG_KEYS.
	{"ship.mergeApproval", "none", false},
	{"ship.mergeApprovalPaths", []any{}, false},
	{"round.scope", "milestone", false},
	{"round.roster", []any{"ben", "dana", "nia", "kit"}, false},
	{"round.brief", "", false},
	{"round.sharedPaths", []any{}, false},
	{"round.tier", "standard", false},
	{"round.tiers.claude.light", "haiku", false},
	{"round.tiers.claude.standard", "", false}, // empty: models.worker
	{"round.tiers.claude.heavy", "opus", false},
	{"round.tiers.codex.light", "", false},
	{"round.tiers.codex.standard", "", false},
	{"round.tiers.codex.heavy", "", false},
	{"round.stallMinutes", json.Number("30"), false},
	{"issues.labels.needsHuman", "needs-human", false},
	{"work.codexCommand", "", false}, // empty: DefaultCodexCommand in internal/worker

	{"orchestrator.handoffThreshold", json.Number("75"), false},
	{"orchestrator.stateMaxAgeSeconds", json.Number("120"), false},
	{"orchestrator.handoffMaxAgeSeconds", json.Number("900"), false},
	{"orchestrator.handoffMaxBlocks", json.Number("2"), false},
	// D2 keepalive keys: silent defaults, read by `rota keepalive run`.
	{"orchestrator.keepaliveMaxRestarts", json.Number("10"), false},
	{"orchestrator.keepaliveBreaker", json.Number("3"), false},
	{"orchestrator.keepaliveBackoffSeconds", json.Number("5"), false},
	{"orchestrator.restartPrompt", "Continue as orchestrator: read the handoff injected at session start, run rota round status, and resume the round.", false},
	{"orchestrator.escalateIssue", json.Number("0"), false},
	// #19 launcher key: read by `rota orchestrate` and bare `rota`.
	{"orchestrator.harness", "claude", false},
	// D4 usage-switch keys: silent defaults, read by the Stop hook and `rota keepalive run`.
	{"orchestrator.switchOnUsage", false, false},
	{"orchestrator.usageThreshold", json.Number("90"), false},
	// D3 usage-limit keys: silent defaults, read by `rota limit watch` and the
	// watcher inside `rota keepalive run`.
	{"limits.mode", "switch", false},
	{"limits.resumeMarginSeconds", json.Number("60"), false},
	{"limits.fallbackSleepSeconds", json.Number("1800"), false},
	{"limits.maxResumes", json.Number("3"), false},
	{"limits.resumePrompt", "The usage limit has reset. Continue where you left off.", false},
}

// PythonKeys is how many leading rows of Keys are CONFIG_KEYS.
const PythonKeys = 54

// backlogBackends are the accepted values of backlog.backend.
var backlogBackends = []string{"file", "issues"}

// walk is Python's _walk: a missing key, a non-object parent or a JSON null
// at any segment counts as absent.
func walk(cfg any, dotted string) (any, bool) {
	cur := cfg
	for _, seg := range strings.Split(dotted, ".") {
		obj, ok := cur.(*jsonx.Object)
		if !ok {
			return nil, false
		}
		v, ok := obj.Get(seg)
		if !ok || v == nil {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

// Value returns the value of a dotted key in cfg, or the key's default when
// the key is missing or JSON null at any segment (config.Lookup, by contrast,
// reports a null as present). It is an error for a key that is not in Keys.
func Value(cfg any, dotted string) (any, error) {
	for _, k := range Keys {
		if k.Name != dotted {
			continue
		}
		if v, ok := walk(cfg, dotted); ok {
			return v, nil
		}
		if list, isList := k.Default.([]any); isList {
			return append([]any{}, list...), nil // a fresh slice, so callers cannot edit the table
		}
		return k.Default, nil
	}
	return nil, fmt.Errorf("unknown config key %q", dotted)
}

// Backend returns the configured backlog backend, "file" or "issues". Any
// other value is an error naming it, as hvlib_config.backlog_backend does.
func Backend(cfg any) (string, error) {
	v, err := Value(cfg, "backlog.backend")
	if err != nil {
		return "", err
	}
	if s, ok := v.(string); ok {
		for _, b := range backlogBackends {
			if s == b {
				return s, nil
			}
		}
	}
	return "", fmt.Errorf("invalid backlog.backend '%s' (expected file|issues)", pyStr(v))
}

// Label returns the tracker label name for a role under issues.labels, such
// as "inProgress" or "types.bug". The "inProgress" role falls back to the
// legacy issues.label when issues.labels.inProgress is unset. A non-string
// setting or an unknown role yields "", which no tracker label equals; Python
// would hand back the raw value or raise KeyError there.
func Label(cfg any, role string) string {
	if role == "inProgress" {
		if _, ok := walk(cfg, "issues.labels.inProgress"); !ok {
			if legacy, ok := walk(cfg, "issues.label"); ok {
				s, _ := legacy.(string)
				return s
			}
		}
	}
	v, err := Value(cfg, "issues.labels."+role)
	if err != nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

// pyStr is Python's str(v) for a decoded JSON value, as an f-string shows it.
func pyStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return pyRepr(v)
}

// pyRepr is Python's repr(v) for a decoded JSON value. String escaping covers
// the common cases (quotes, backslash, control characters), not every
// non-printable code point.
func pyRepr(v any) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case bool:
		if x {
			return "True"
		}
		return "False"
	case json.Number:
		raw, err := jsonx.MarshalCompact(x)
		if err != nil {
			return x.String()
		}
		return string(raw)
	case string:
		return pyQuote(x)
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = pyRepr(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *jsonx.Object:
		var parts []string
		for _, k := range x.Keys() {
			e, _ := x.Get(k)
			parts = append(parts, pyQuote(k)+": "+pyRepr(e))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return fmt.Sprint(v)
}

func pyQuote(s string) string {
	q := byte('\'')
	if strings.Contains(s, "'") && !strings.Contains(s, `"`) {
		q = '"'
	}
	var b strings.Builder
	b.WriteByte(q)
	for _, r := range s {
		switch {
		case r == rune(q) || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			b.WriteString(`\x` + fmt.Sprintf("%02x", r))
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte(q)
	return b.String()
}
