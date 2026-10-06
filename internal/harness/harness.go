// Package harness is the seam between rota and the coding agents it starts. A
// harness is one agent CLI: it owns its launch line and model placeholder, the
// flag that would resume an old session, how a payload sent into its pane is
// signed, its account model (CLAUDE_CONFIG_DIR versus CODEX_HOME) and its
// readiness check. Callers (worker dispatch, round assign, the herdr host,
// doctor, the orchestrator launcher) pick an adapter once and call it; none of
// them branches on a kind string.
//
// Two harnesses run workers (Claude and Codex). The orchestrator can also run
// in Hermes and opencode, which only have the orchestrator facet: the worker
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
	// Default is the kind of a slot that records none.
	Default = Claude
)

// Kinds lists the worker harness kinds.
var Kinds = []string{Claude, Codex}

// ModelPlaceholder marks where a custom launch command takes the model.
const ModelPlaceholder = "{model}"

// Valid reports whether s is a worker harness kind.
func Valid(s string) bool {
	for _, k := range Kinds {
		if k == s {
			return true
		}
	}
	return false
}

// KindList is Kinds as prose for a usage error: "claude or codex".
func KindList() string { return strings.Join(Kinds, " or ") }

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
	}
	return nil, false
}

// ForBinary is Lookup for the basename of a launch binary: the herdr host
// starts an agent by the kind its binary is.
func ForBinary(base string) (Harness, bool) {
	for _, k := range Kinds {
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

// Account is the per-slot account a launch can carry.
type Account struct{ ConfigDir, CodexHome string }

// ResumeHit is a token of a launch command that reopens an earlier session.
// Noun prefixes it in a message ("the subcommand ").
type ResumeHit struct{ Token, Noun string }

// PreflightOpts are what a worker harness needs to check a slot before
// anything is marked or killed.
type PreflightOpts struct {
	Slot string
	// Accept lets the slot through an unsupported CLI version, with a warning.
	Accept bool
	// Herdr is whether the round's host is herdr.
	Herdr bool
	// Worktree is the slot's worktree, which a seeded home must trust.
	Worktree string
	// CommonDir is the git common dir, asked for only by a harness that keeps
	// per-slot state beside it.
	CommonDir func() (string, error)
	// Accounts are the configured Codex homes (work.codexAccounts); none
	// means the default Codex home. Account is the one the slot ran under
	// last, Load how many other codex slots hold each account.
	Accounts []HomeAccount
	Account  string
	Load     map[string]int
}

// HomeAccount is one named CODEX_HOME of work.codexAccounts.
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
	// AccountEnv is the environment that selects the slot's account.
	AccountEnv(a Account) []string
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
	// Command is the argv that starts the agent, without its first prompt.
	Command(cfg any) ([]string, error)
	// Prompt starts the orchestrate skill in the harness's own invocation
	// syntax: Claude Code takes a slash command, Codex a `$skill` mention.
	Prompt() string
}

// Orchestrators is the launch table, in the order the error message lists it.
func Orchestrators() []Orchestrator {
	return []Orchestrator{claude{}, codex{}, hermes{}, opencode{}}
}
