// Package harness is the seam between rota and the coding agents it starts. A
// harness is one agent CLI: it owns its launch line and model placeholder, the
// flag that would resume an old session, how a payload sent into its pane is
// signed, its account model (CLAUDE_CONFIG_DIR versus CODEX_HOME) and its
// readiness check. Callers (worker dispatch, round assign, the herdr host,
// doctor, the orchestrator launcher) pick an adapter once and call it; none of
// them branches on a kind string.
//
// Three harnesses run workers (Claude, Codex and OpenCode). The orchestrator
// can also run in Hermes, which only has the orchestrator facet: the worker
// and orchestrator sets differ (#33), and both are views over this package.
//
// The package imports no other rota package but config and shlex, so doctor,
// host, worker and orchestrate can all depend on it.
package harness

import (
	"context"
	"fmt"
	"strings"
)

// Harness kinds of a worker.
const (
	Claude = "claude"
	Codex  = "codex"
	// OpenCode has no lower-case sibling: the binary and the kind are both
	// "opencode".
	OpenCode = "opencode"
	// Default is the kind of a slot that records none.
	Default = Claude
)

// kinds is the worker harness kinds, in the order messages list them.
var kinds = []string{Claude, Codex, OpenCode}

// Kinds lists the worker harness kinds. Every list of kinds outside this
// package (the round.workerKind enum, the harness: label check, herdr's
// binary-to-kind map, usage errors) reads it.
func Kinds() []string { return append([]string(nil), kinds...) }

// ModelPlaceholder marks where a custom launch command takes the model.
const ModelPlaceholder = "{model}"

// Valid reports whether s is a worker harness kind.
func Valid(s string) bool {
	for _, k := range kinds {
		if k == s {
			return true
		}
	}
	return false
}

// KindList is Kinds as prose for a usage error: "claude, codex or opencode".
func KindList() string {
	if len(kinds) < 2 {
		return strings.Join(kinds, "")
	}
	return strings.Join(kinds[:len(kinds)-1], ", ") + " or " + kinds[len(kinds)-1]
}

// Lookup returns the worker adapter for kind; "" is Default.
func Lookup(kind string) (Harness, bool) {
	if kind == "" {
		kind = Default
	}
	switch kind {
	case Claude:
		return claude{}, true
	case Codex:
		return codex{}, true
	case OpenCode:
		return opencode{}, true
	}
	return nil, false
}

// Resolve is Lookup for a kind that must have an adapter: "" and a kind that is
// not a worker harness (a corrupt slot or ledger record) resolve to the
// Default one, the way those records always read.
func Resolve(kind string) Harness {
	if h, ok := Lookup(kind); ok {
		return h
	}
	h, _ := Lookup(Default)
	return h
}

// IsDefault reports whether kind is the Default one ("" is).
func IsDefault(kind string) bool { return kind == "" || kind == Default }

// DefaultOrchestrator is the orchestrator harness of a project that sets none.
const DefaultOrchestrator = Claude

// ForBinary is Lookup for the basename of a launch binary: the herdr host
// starts an agent by the kind its binary is.
func ForBinary(base string) (Harness, bool) {
	for _, k := range kinds {
		if k == base {
			return Lookup(k)
		}
	}
	return nil, false
}

// Class is how a Refusal maps onto a caller's exit.
type Class int

const (
	// Usage: the configuration or flags are wrong.
	Usage Class = iota
	// Unavailable: a tool, login or file the harness needs is missing.
	Unavailable
	// Refused: the harness will not start in this state; BlockedBy says why.
	Refused
	// Resolution: something the call names does not exist.
	Resolution
)

// Refusal is a harness that cannot start or send, in a form each caller maps
// onto its own exit codes.
type Refusal struct {
	Class     Class
	BlockedBy string
	Msg, Hint string
}

func (r *Refusal) Error() string { return r.Msg }

func refuse(c Class, format string, a ...any) *Refusal {
	return &Refusal{Class: c, Msg: fmt.Sprintf(format, a...)}
}

// Result is what a finished command left behind.
type Result struct {
	Stdout, Stderr string
	ExitCode       int
}

// Probe is how a harness reaches the outside world, so tests need no real
// binary. Run reports a command that ran and failed as a Result with ExitCode
// set; an error means it could not run at all.
type Probe struct {
	Look func(name string) (string, bool)
	Run  func(ctx context.Context, bin string, args, env []string) (Result, error)
}

// AccountEntry is one configured account of a kind: its name and the
// directory that holds its login (a Claude config dir, a Codex home).
type AccountEntry struct{ Name, Dir string }

// AccountList is a kind's configured accounts and the config key they are
// read from.
type AccountList struct {
	Key     string
	Entries []AccountEntry
}

// PaneVocab is a kind's own pane texts, as regex sources. Every field is read
// case-insensitively except BadModel (a dialog whose model id is captured as
// written) and Gutter (not a regex). The zero value is a kind that reads like Claude Code.
type PaneVocab struct {
	// Retry marks a retry in flight (busy, checked before limits); Busy marks a
	// turn running (busy, checked after limits and dialogs).
	Retry, Busy []string
	// Limits are usage-limit messages beyond the shared ones. AuthDead is the
	// one of them that a human must fix (a dead key): no reset clock exists.
	Limits   []string
	AuthDead string
	// BadModel is the dialog for a tier model the provider refuses; group 1 is
	// the model id. The pane is BLOCKED.
	BadModel string
	// Gutter: the TUI echoes user text under a "┃" gutter and may gutter its own
	// reply, so a sentinel may follow it.
	Gutter bool
}

// TierRule is how a kind's round.tiers.<kind> map is read.
type TierRule struct {
	// Required: the tier map must be configured (a kind that has no model of
	// its own to fall back on).
	Required bool
	// StandardFallback is the config key whose model fills an unset standard
	// tier, "" for none.
	StandardFallback string
}

// AgentRole is one rota subagent role as a harness renders it.
type AgentRole struct {
	Name, Description, Instructions, Effort string
}

// AgentFile is one file `rota agents write` emits, relative to the project
// root.
type AgentFile struct{ Path, Content string }

// AgentMarker is the phrase every generated agent file's header holds. A file
// without it was written by hand and is left alone.
const AgentMarker = "generated by rota"

// Doctor says what `rota doctor` probes for a kind. The zero value means the
// kind has no check of its own (Claude's are the accounts, hook and statusline
// checks). It carries presence and launch probes only: no version is compared
// (DECISIONS, "No harness version pins").
type Doctor struct {
	// Check names the report line, Binary is looked up on PATH and
	// InstallHint is the fix when it is absent.
	Check, Binary, InstallHint string
	// Title is the kind as prose in a report line ("Codex").
	Title string
	// Runnable runs the binary's own version probe against a home and returns
	// the version it printed for display; ok is false when it cannot run.
	Runnable func(ctx context.Context, p Probe, bin, home string) (version string, ok bool)
	// Home probes one account home: its login and, under herdr, the
	// integration. Nil: the kind has no per-home state.
	Home func(ctx context.Context, p Probe, bin, herdr string, h Home) []Finding
	// LoginFix and IntegrationFix are the commands that cure the findings of
	// Home, run under HomeEnv (`CODEX_HOME=<dir> `) when the home is named.
	LoginFix, IntegrationFix string
	HomeEnv                  string
	// Unguarded: rota's PreToolUse guard hook does not cover the kind's
	// workers.
	Unguarded bool
}

// Account is the per-slot account a launch can carry: a Claude config dir, or
// the home of a kind with its own account model (CODEX_HOME,
// OPENCODE_CONFIG_DIR).
type Account struct{ ConfigDir, Home string }

// ResumeHit is a token of a launch command that reopens an earlier session.
// Noun prefixes it in a message ("the subcommand ").
type ResumeHit struct{ Token, Noun string }

// PreflightOpts are what a worker harness needs to check a slot before
// anything is marked or killed.
type PreflightOpts struct {
	Slot string
	// Launch is the launch line the slot will run; a harness probes the flags
	// it holds.
	Launch string
	// Herdr is whether the round's host is herdr.
	Herdr bool
	// Worktree is the slot's worktree, which a seeded home must trust.
	Worktree string
	// CommonDir is the git common dir, asked for only by a harness that keeps
	// per-slot state beside it.
	CommonDir func() (string, error)
	// Accounts are the kind's configured homes (work.codexAccounts,
	// work.opencodeAccounts); none means the default home. Account is the one
	// the slot ran under last, Load how many other slots of the kind hold each.
	Accounts []HomeAccount
	Account  string
	Load     map[string]int
}

// HomeAccount is one named account home of a kind: a CODEX_HOME of
// work.codexAccounts, an OPENCODE_CONFIG_DIR of work.opencodeAccounts.
type HomeAccount struct{ Name, Home string }

// Setup is what a passed preflight leaves for the dispatch. A non-empty Home
// means the slot's account is that home and the claude config dir is not its
// business; a codex slot on the default home leaves it empty.
type Setup struct {
	Home string
	// Account names the configured Codex account the slot got ("" on the
	// default home); the dispatch records it so the next pick keeps it.
	Account string
	// StateDir is the slot's own state directory (the codex prompt key).
	StateDir string
	// Worktree is the slot's worktree, trusted on a codex launch line.
	Worktree string
	Version  string
	Warnings []string
}

// Harness is one worker agent.
type Harness interface {
	Kind() string
	// CommandKey is the config key that overrides the launch line.
	CommandKey() string
	// Launch is the launch line for a chosen model ("" keeps the default).
	// A *Refusal of class Usage means the configuration cannot launch.
	Launch(cfg any, model string) (string, error)
	// NeedsModel reports whether a launch needs a tier model.
	NeedsModel(cfg any) bool
	// ModelApplies reports whether a chosen model reaches the launch line.
	ModelApplies(cfg any) bool
	// Resume finds a token of launch that reopens the previous session, which
	// a task dispatch must not do. An error means launch cannot be parsed.
	Resume(launch string) (ResumeHit, error)
	// SoloRefusal is why the harness cannot run as a subagent of a solo
	// round, "" when it can.
	SoloRefusal() string
	// WorkAccounts reports whether work.accounts (Anthropic's) rotates the
	// slot's account; a harness with its own account model says no.
	WorkAccounts() bool
	// RawPaste reports whether a brief pasted into the kind's pane must keep
	// its line feeds as they are: tmux pastes a CR for each, which the TUI
	// drops and the brief arrives as one line.
	RawPaste() bool
	// AccountEnv is the environment that selects the slot's account.
	AccountEnv(a Account) []string
	// Accounts is the kind's configured accounts and the config key they come
	// from (work.accounts, work.codexAccounts).
	Accounts(cfg any) AccountList
	// UsageMeter reports whether the kind's accounts expose a usage reading.
	// One that does not is balanced from the limits the watcher logged.
	UsageMeter() bool
	// LimitPhrases are the usage-limit messages the kind's session prints
	// about itself, as case-insensitive regex sources.
	LimitPhrases() []string
	// PaneVocab is what the kind's pane says that no other kind's does. The
	// classifier applies it to that kind's slots only: a worker of another kind
	// that echoes the text (reading this repo's source) must not read as limited.
	PaneVocab() PaneVocab
	// Tiers is how the kind's round.tiers map is read.
	Tiers() TierRule
	// Configured is whether the project uses the kind at all.
	Configured(cfg any) bool
	// AgentFiles are the files `rota agents write` emits for a role: none for
	// a kind without a subagent format. model is "" when the kind has no tier
	// map. An error is a role the kind cannot express.
	AgentFiles(r AgentRole, model string) ([]AgentFile, error)
	// Doctor is what `rota doctor` probes for the kind.
	Doctor() Doctor
	// CheckLaunch refuses a launch line the harness cannot run safely.
	CheckLaunch(launch string) error
	// Preflight checks, and prepares, a slot before its old session is killed.
	Preflight(ctx context.Context, p Probe, o PreflightOpts) (Setup, error)
	// Prepare is the last edit of the launch line (a hook) and the key that
	// signs the session's payloads; a nil key means payloads go unsigned.
	Prepare(launch string, exe func() (string, error), s Setup) (string, []byte, error)
	// RelayKey loads the key of a running session, nil when it signs nothing.
	RelayKey(commonDir func() (string, error), slot string) ([]byte, error)
	// Sign ends payload with its signature; a nil key returns it unchanged.
	Sign(key []byte, payload string) string
}

// Orchestrator is one agent the orchestrator can run in.
type Orchestrator interface {
	Name() string
	// WorkAccounts reports whether work.accounts (Anthropic's) names the
	// account the agent starts under.
	WorkAccounts() bool
	// Command is the argv that starts the agent, without its first prompt.
	Command(cfg any) ([]string, error)
	// Prompt starts the orchestrate skill in the harness's own invocation
	// syntax: Claude Code takes a slash command, Codex a `$skill` mention.
	Prompt() string
}

// sharedLimitPhrases are the usage-limit messages of the harnesses that have
// no wording of their own. The worker poll and the usage-limit watcher match
// the same list, so there is one source.
var sharedLimitPhrases = []string{
	`reached your usage limit`,
	`usage limit reached`,
	`You'?ve hit your (?:usage )?limit`,
	`limit (?:will )?reset[s]? at`,
	`Approaching (?:your )?usage limit`,
}

// SharedLimitPhrases returns the default list a kind's LimitPhrases starts
// from.
func SharedLimitPhrases() []string { return append([]string(nil), sharedLimitPhrases...) }

// AllLimitPhrases is the union of every kind's LimitPhrases, in kind order
// without repeats: a pane is classified without knowing its kind.
func AllLimitPhrases() []string {
	var out []string
	seen := map[string]bool{}
	for _, k := range kinds {
		for _, p := range Resolve(k).LimitPhrases() {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}

// AllPaneLimits is the union of every kind's PaneVocab.Limits, without repeats:
// a prefilter for a pane watcher that cannot know a pane's kind.
func AllPaneLimits() []string {
	var out []string
	seen := map[string]bool{}
	for _, k := range kinds {
		for _, p := range Resolve(k).PaneVocab().Limits {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}

// Orchestrators is the launch table, in the order the error message lists it.
func Orchestrators() []Orchestrator {
	return []Orchestrator{claude{}, codex{}, hermes{}, opencode{}}
}
