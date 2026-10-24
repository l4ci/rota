package config

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/jsonx"
)

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

var (
	issueSources   = []string{SourceFile, SourceGitHub, SourceGitLab, SourceForge, SourceJira, SourceLinear, SourceItsaplan}
	forgeProviders = []string{ForgeAuto, ForgeGitHub, ForgeGitLab, ForgeLocal}
)

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
		v, _ := effectiveValue(cfg, k)
		return v, nil
	}
	return nil, fmt.Errorf("unknown config key %q", dotted)
}

// effectiveValue resolves a schema key from the merged config. The boolean
// reports whether the value was configured rather than supplied by its default.
func effectiveValue(cfg any, k Key) (any, bool) {
	if v, ok := walk(cfg, k.Name); ok {
		return v, true
	}
	return Default(k), false
}

// Issue sources: the values of issues.source. SourceIssues is not one of them:
// it is what IssueSource reports for an unmigrated legacy config that says
// "issues on the tracker" without naming it (the origin remote decides).
const (
	SourceFile     = "file"
	SourceGitHub   = "github"
	SourceGitLab   = "gitlab"
	SourceJira     = "jira"
	SourceLinear   = "linear"
	SourceItsaplan = "itsaplan"
	// SourceForge: the issues live on each repo's own forge (an umbrella whose
	// sub-repos sit on different hosts).
	SourceForge  = "forge"
	SourceIssues = "issues"
)

// Forge providers: the values of forge.provider.
const (
	ForgeAuto   = "auto"
	ForgeGitHub = "github"
	ForgeGitLab = "gitlab"
	ForgeLocal  = "local"
)

// IssueSource returns where the issues live: issues.source, one of file,
// github, gitlab, jira, linear or itsaplan. Any other value is an error naming it.
//
// While issues.source is unset and the legacy keys remain (an upgraded project
// before rota migrate source), the legacy keys decide, so such a project never
// flips to file mode: backlog.backend "file" is file; "issues" is the provider
// named by issues.provider, or SourceIssues when that is auto or unset (the
// origin remote decides).
func IssueSource(cfg any) (string, error) {
	if v, ok := walk(cfg, "issues.source"); ok {
		if s, ok := v.(string); ok && slices.Contains(issueSources, s) {
			return s, nil
		}
		return "", fmt.Errorf("invalid issues.source %s (expected %s)", jsonText(v), strings.Join(issueSources, "|"))
	}
	if v, ok := walk(cfg, LegacyBackendKey); ok {
		switch v {
		case "file":
			return SourceFile, nil
		case "issues":
			if p, ok := walk(cfg, LegacyProviderKey); ok && (p == "github" || p == "gitlab") {
				return p.(string), nil
			}
			return SourceIssues, nil
		}
		return "", fmt.Errorf("invalid backlog.backend %s (expected file|issues)", jsonText(v))
	}
	return SourceFile, nil
}

// ForgeProvider returns forge.provider: auto, github, gitlab or local. Any
// other value is an error naming it. While forge.provider is unset, a legacy
// issues.provider of github or gitlab stands in for it, as the old PR verbs
// read it as their fallback.
func ForgeProvider(cfg any) (string, error) {
	if v, ok := walk(cfg, "forge.provider"); ok {
		if s, ok := v.(string); ok && slices.Contains(forgeProviders, s) {
			return s, nil
		}
		return "", fmt.Errorf("invalid forge.provider %s (expected %s)", jsonText(v), strings.Join(forgeProviders, "|"))
	}
	if p, ok := walk(cfg, LegacyProviderKey); ok && (p == "github" || p == "gitlab") {
		return p.(string), nil
	}
	return ForgeAuto, nil
}

// Backend is the backlog backend derived from issues.source: "file" for the
// file source, "issues" for any tracker. An invalid source is an error.
func Backend(cfg any) (string, error) {
	src, err := IssueSource(cfg)
	if err != nil {
		return "", err
	}
	if src == SourceFile {
		return "file", nil
	}
	return "issues", nil
}

// Label returns the tracker label name for a role under issues.labels, such
// as "inProgress" or "types.bug". A non-string setting or an unknown role yields "", which no tracker label equals; Python
// would hand back the raw value or raise KeyError there. An explicit "" is
// returned as is: it does not fall back to the default, and callers do not
// substitute one.
func Label(cfg any, role string) string {
	v, err := Value(cfg, "issues.labels."+role)
	if err != nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

// jsonText shows a decoded JSON value for the invalid-value errors: a string
// comes quoted with its printable runes intact, a number, list or object as
// compact JSON. Decoded values always encode, so the error is dropped.
func jsonText(v any) string {
	if s, ok := v.(string); ok {
		return strconv.Quote(s)
	}
	raw, _ := jsonx.MarshalCompact(v)
	return string(raw)
}

// Choice is one answer to a Prompt: Value is what config set stores (JSON, so
// "true" is a boolean), Desc is the line shown beside it.
type Choice struct {
	Value string
	Desc  string
}

// Prompt is one question of the interactive setup (rota setup). The values, the
// order and the default come from the schema key (Key.Choices, or true and
// false for a bool, the default first); Help only words each value for this
// question. TestPromptsMatchSchema pins that every value has a line.
type Prompt struct {
	Key   string
	Title string
	// Help maps each choice value to the line shown beside it.
	Help map[string]string
	// Choices is built from the schema and Help at init.
	Choices []Choice
	// IfKey, when set, makes the question conditional: it is asked only when
	// the answer (or default) for IfKey equals IfValue.
	IfKey, IfValue string
	// Optional marks a question whose absence is meaningful: Enter (or --yes)
	// leaves the key unset, and only an explicit answer is written.
	Optional bool
	// Free marks a question answered with free text, not a choice: a string
	// key or a list key (a JSON list, or one command). It has no Choices.
	Free bool
	// Roles are the round.role values whose setup asks the question; nil is
	// every role. The support path (#1774) asks only machine-local questions:
	// the project-owned answers arrive with the clone's tracked config.
	Roles []string
}

// mainOnly marks a question the support machine never asks.
var mainOnly = []string{"main"}

// Prompts is the setup's questions, in the order they are asked.
var Prompts = []Prompt{
	{Key: "round.role", Title: "Is this the main machine or a support machine?", Help: map[string]string{
		"main":    "the main orchestrator: sets the project up and merges",
		"support": "a support orchestrator for a project main already set up (round.support)",
	}},
	{Key: "issues.source", Title: "Where do the issues live?", Roles: mainOnly, Help: map[string]string{
		"file":     "BACKLOG.md in the repo",
		"github":   "GitHub issues (gh)",
		"gitlab":   "GitLab issues (glab)",
		"forge":    "each repo's own forge (umbrellas on mixed hosts)",
		"jira":     "Jira (needs a rota connect connection)",
		"linear":   "Linear (needs a rota connect connection)",
		"itsaplan": "It's a Plan (needs a rota connect connection)",
	}},
	{Key: "work.isolation", Title: "How is each piece of work isolated?", Roles: mainOnly, Help: map[string]string{
		"branch":   "a feature branch in this checkout",
		"worktree": "a separate git worktree per item",
	}},
	{Key: "work.mergeStrategy", Title: "How does finished work land?", Roles: mainOnly, Help: map[string]string{
		"direct": "merge straight into the base branch",
		"pr":     "open a pull request",
	}},
	{Key: "round.dispatch", Title: "Where do workers run?", Help: map[string]string{
		"subagent": "in-process subagents (rounds detect herdr or tmux)",
		"tmux":     "separate Claude Code sessions in tmux",
		"herdr":    "separate Claude Code sessions in herdr",
	}},
	{Key: "autonomy.level", Title: "How much may rota chain on its own?", Help: map[string]string{
		"off":       "skills only suggest the next step",
		"roundtrip": "run one item from brainstorm to ship, then stop",
		"auto":      "keep taking the next ready item; rounds run the autopilot",
	}},
	{Key: "ship.review", Title: "How deep a review before shipping?", Roles: mainOnly, Help: map[string]string{
		"full":  "run /rota-review",
		"light": "Spec reviewer only",
		"none":  "skip the review",
	}},
	{Key: "ship.qa", Title: "Run QA before shipping?", Roles: mainOnly, Help: map[string]string{
		"false": "no",
		"true":  "yes, run /rota-qa",
	}},
	{Key: "round.harnesses", Title: "Which harnesses may round workers use? The first is the default (claude, codex, opencode; one name or a JSON list)", Optional: true, Free: true},
	{Key: "orchestrator.harness", Title: "Which harness runs the round orchestrator?", Optional: true, Help: map[string]string{
		"claude":   "Claude Code",
		"codex":    "Codex",
		"hermes":   "Hermes",
		"opencode": "OpenCode",
	}},
	{Key: "git.baseBranch", Title: "Which branch do the skills merge into?", Optional: true, Free: true, Roles: mainOnly},
	{Key: "test.fast", Title: "Which command is the quick per-task check?", Optional: true, Free: true, Roles: mainOnly},
	{Key: "test.full", Title: "Which command runs the full suite?", Optional: true, Free: true, Roles: mainOnly},
	{Key: "round.envSetup", Title: "Which command installs dependencies in a new worker worktree?", Optional: true, Free: true, Roles: mainOnly},
	{Key: "round.sharedDirs", Title: "Which dependency directories do worker worktrees share with the main checkout?", Optional: true, Free: true, Roles: mainOnly},
}

func init() {
	for i := range Prompts {
		p := &Prompts[i]
		key, ok := SchemaKey(p.Key)
		if !ok {
			panic("config: prompt for unknown key " + p.Key)
		}
		values := key.Choices
		if key.Type == TypeBool {
			values = []string{"true", "false"}
			if key.Default == false {
				values = []string{"false", "true"}
			}
		}
		p.Choices = nil
		for _, v := range values {
			p.Choices = append(p.Choices, Choice{v, p.Help[v]})
		}
	}
}

// SchemaKey is the schema row for name.
func SchemaKey(name string) (Key, bool) {
	for _, key := range Keys {
		if key.Name == name {
			return key, true
		}
	}
	return Key{}, false
}

// DefaultChoice is the default of p's key as a Choice value: strings as they
// are, booleans as "true" or "false".
// An Optional prompt has none: the key stays unset unless answered.
func (p Prompt) DefaultChoice() string {
	if p.Optional {
		return ""
	}
	for _, k := range Keys {
		if k.Name == p.Key {
			return fmt.Sprint(k.Default)
		}
	}
	return ""
}

// Valid is whether v is one of p's choice values.
func (p Prompt) Valid(v string) bool {
	if p.Free {
		return v != "" && Validate(p.Key, v) == nil
	}
	for _, c := range p.Choices {
		if c.Value == v {
			return true
		}
	}
	return false
}

// RoleNames are the round.role values whose setup asks p.
func (p Prompt) RoleNames() []string {
	if p.Roles != nil {
		return p.Roles
	}
	k, _ := SchemaKey("round.role")
	return k.Choices
}

// ForRole is whether the setup for role asks p.
func (p Prompt) ForRole(role string) bool {
	return slices.Contains(p.RoleNames(), role)
}

// PromptFor is the prompt for key, or nil.
func PromptFor(key string) *Prompt {
	for i := range Prompts {
		if Prompts[i].Key == key {
			return &Prompts[i]
		}
	}
	return nil
}
