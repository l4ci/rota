// Package doctor is the logic behind `rota doctor`: one read-only preflight
// check per thing a parallel round depends on (git, jq, the dispatch host, the
// forge CLI, the worker accounts, herdr's agent integration, the installed skills).
//
// Nothing here reaches os/exec or the real PATH directly: the caller injects
// Exec and Look, so tests need no real herdr, gh or tmux. A missing tool is a
// failed check, never an error.
package doctor

import (
	"context"
	"net"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/hook"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/proc"
	"github.com/l4ci/rota/internal/skills"
	"github.com/l4ci/rota/internal/stalebin"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// Check statuses.

// Check statuses.
const (
	Pass = "pass"
	Fail = "fail"
	Skip = "skip"
	// Warn is a finding that does not fail the report: the round can run, but
	// the machine is about to make it fail (low disk).
	Warn = "warn"
)

// Check is one line of the report.
type Check struct {
	Name   string
	Status string
	Detail string
	Hint   string // set on every Fail
}

// Report is every check, in order.
type Report struct{ Checks []Check }

// OK is true when no check failed.
func (r Report) OK() bool {
	for _, c := range r.Checks {
		if c.Status == Fail {
			return false
		}
	}
	return true
}

// Result is what a finished command left behind.
type Result = proc.Result

// Exec runs bin (a path Look returned) in dir with extraEnv added to the
// process environment. A command that ran and exited non-zero is a Result
// with ExitCode set and a nil error; err means it could not run at all.
type Exec func(ctx context.Context, bin string, args []string, extraEnv []string, dir string) (Result, error)

// StaleClaim is a remote claim with no recent branch or PR activity.
type StaleClaim struct {
	Issue, Holder string
	Last          time.Time
}

// Input is everything Run needs from the outside.
type Input struct {
	// Getenv and Dial identify the app-associated Core without executable probes.
	Getenv func(string) string
	Dial   func(context.Context, string) (net.Conn, error)

	// Labels is the bounded remote label scan; nil outside an issue backend.
	Labels *LabelState
	// StaleClaims are remote claims past their activity threshold; empty adds no rows.
	StaleClaims []StaleClaim
	// StaleClaimWarnings explain why the remote scan could not finish.
	StaleClaimWarnings []string

	Dir  string // working directory: where git runs
	Home string // expands a leading "~/" in a configDir

	// From the project config when .rota/ exists; zero values otherwise.
	Dispatch     string           // round.dispatch
	RegistrySolo bool             // the recorded round runs on the solo host (worker.IsSolo)
	RoundHost    host.Kind        // the host the round's verbs drive (worker.ResolveHost), resolved with no project too
	Tracker      tracker.Settings // tracker.SettingsFromConfig over the project config
	// Tracker.Source is issues.source, "" outside a rota project (an unmigrated
	// legacy config reads as config.SourceIssues). SourceProblem is the reason
	// forge.provider or issues.source holds an invalid value ("" when valid);
	// the settings are then left zero. TeamProblem is why the Linear team or
	// It's a Plan project could not be read through a working connection ("" when
	// it was, or was not tried).
	SourceProblem string
	TeamProblem   string
	Accounts      []config.Account
	// Credentials reads each account's login; the zero value reads files only.
	Credentials worker.Credentials
	// OrchestratorConfigDir is the orchestrator's CLAUDE_CONFIG_DIR, "" when unset.
	OrchestratorConfigDir string
	SwitchOnUsage         bool // orchestrator.switchOnUsage (D4)
	// Kinds is what each worker harness with a check of its own (Doctor) has
	// configured, by kind.
	Kinds map[string]KindInput

	// Connection is the project's issues.connection and what checking it
	// found; nil (no connection named) adds no line.
	Connection *ConnCheck

	// AgentProblems are the generated subagent files that are missing or have
	// drifted from config (agents.Problems); empty adds no line.
	AgentProblems []string
	// AgentStoreFault is set when AgentProblems comes from the saved prompt
	// store (damaged or from a newer rota), which `rota agents write` cannot fix.
	AgentStoreFault bool

	// Gitignore is the project's .gitignore text; nil when the file does not
	// exist, which is not drift (init creates it) and adds no line.
	Gitignore *string

	// Skills is the state of the installed skill sets (skills.Set.Status);
	// nil when nothing was read.
	Skills *skills.Report

	// StaleMilestoneFiles are the .rota/milestones/*.md names left over while
	// milestones live on the tracker; empty adds no line.
	StaleMilestoneFiles []string

	// StateDir is the rota state dir (the project registry lives there); ""
	// adds no line.
	StateDir string

	// LegacyDir is the directory that still holds the old state folder with no .rota/ (a
	// project `rota migrate hv` has not moved); "" otherwise.
	LegacyDir string

	// ConfigDir is the global rota config dir in use and why ("override",
	// "xdg" or "default"); ConfigDirWarning says a relative ROTA_CONFIG_DIR
	// was ignored. Empty ConfigDir adds no line.
	ConfigDir, ConfigDirSource, ConfigDirWarning string

	// ProjectRoot is the directory holding .rota/, "" outside a rota project:
	// the orchestrator checks (statusline, stop-hook) skip without it.
	ProjectRoot string
	// DefaultConfigDir is the user's Claude config dir; the orchestrator checks
	// read it when no account names a configDir of its own.
	DefaultConfigDir string

	// Disk is the free space of the volume the round writes to; nil when it
	// could not be read (no check then). MinFreeDiskPercent is the
	// doctor.minFreeDiskPercent threshold; 0 turns the check off.
	// ScanLeftovers names what rota left behind that would give space back
	// (stale scratch worktrees, leaked temp dirs); it walks the disk, so it
	// runs only once Disk is low. Nil adds no leftovers.
	Disk               *Disk
	MinFreeDiskPercent int
	ScanLeftovers      func() []string
	// CacheDisk is the volume holding the Go build cache when it is not the
	// one Disk reads (the gate's builds grow it); nil adds no line.
	CacheDisk *Disk

	// StaleBinary is set when the installed rota is behind the rota source
	// checkout Dir is in (stalebin.Check); nil adds no line.
	StaleBinary *stalebin.Finding

	// NothingToVerify is set when test.full and test.e2e are both empty under a
	// local verify, so the merge gate would refuse; false adds no line.
	NothingToVerify bool

	// TestSlots is test.slots, the width of the test lock; 0 (no project)
	// skips the test line. SmokeShards is the shard count test/gate.sh would
	// use on this machine, SmokeShardsSource what decided it.
	TestSlots         int
	SmokeShards       int
	SmokeShardsSource string

	// Slots are the live worker slots with a reserved port block; empty skips
	// the ports check. CwdOf answers a process's working directory ("" when
	// unknown); nil reads every directory as unknown.
	Slots []SlotBlock
	CwdOf func(pid int) string

	// ExternalKnowledge is the state of the configured external knowledge
	// clone (knowledge.external); the zero value, no Path, is the feature off.
	ExternalKnowledge ExternalKnowledge

	Exec Exec
	// Look resolves a tool name, or a path with a slash, to an executable path, or
	// false when it is not found. It is the only filesystem and PATH read for tools.
	Look func(name string) (string, bool)
}

// checkEntry is one row of Run's table. run returns the lines the check adds:
// none when an optional check has nothing to say, several for a check that
// reports per finding. leads puts the lines ahead of the whole report.
type checkEntry struct {
	name  string
	run   func() []Check
	leads bool
}

// always wraps a check that reports exactly one line.
func always(fn func() Check) func() []Check {
	return func() []Check { return []Check{fn()} }
}

// optional wraps a check that adds no line when its second result is false.
func optional(fn func() (Check, bool)) func() []Check {
	return func() []Check {
		if c, ok := fn(); ok {
			return []Check{c}
		}
		return nil
	}
}

// checkTable is the report in order. Adding a check is one row here.
func (d *runner) checkTable() []checkEntry {
	return []checkEntry{
		// legacy-state only fires for a project that still holds the old state
		// folder; it leads the report.
		{"legacy state", optional(d.legacyState), true},
		{"git", always(d.git), false},
		{"jq", always(d.jq), false},
		{"host", always(d.host), false},
		{"tracker", always(d.tracker), false},
		{"accounts", always(d.accounts), false},
		{"hook", always(d.hook), false},
		{"statusline", always(d.statusline), false},
		{"stop hook", always(d.stopHook), false},
		{"guard hook", always(d.guardHook), false},
		{"switch", always(d.switchCheck), false},
		{"skills", always(d.skills), false},
		{"harnesses", d.harnessChecks, false},
		{"decisions", optional(d.decisions), false},
		{"connection", optional(d.connection), false},
		// External knowledge unset adds no line: the feature is off, like a
		// healthy disk.
		{"external knowledge", d.externalKnowledgeChecks, false},
		// Only a volume below the threshold adds a disk line.
		{"disk", optional(d.disk), false},
		{"go-cache-disk", optional(d.cacheDisk), false},
		{"config dir", optional(d.configDir), false},
		{"agents", optional(d.agents), false},
		{"gitignore", optional(d.gitignore), false},
		{"labels", optional(d.labels), false},
		{"stale claims", d.staleClaims, false},
		{"stale milestones", optional(d.staleMilestones), false},
		{"ports", optional(d.ports), false},
		{"stale binary", optional(d.staleBinary), false},
		{"verify", optional(d.verify), false},
		{"test lock", optional(d.testLock), false},
		{"state dir", optional(d.stateDir), false},
	}
}

// Run executes every check in the table's order.
func Run(ctx context.Context, in Input) Report {
	d := &runner{in: in, ctx: ctx}
	var lead, checks []Check
	for _, e := range d.checkTable() {
		if e.leads {
			lead = append(lead, e.run()...)
			continue
		}
		checks = append(checks, e.run()...)
	}
	return Report{Checks: append(lead, checks...)}
}

type runner struct {
	in       Input
	ctx      context.Context
	settings *hook.View // read once per run
}

func pass(name, detail string) Check { return Check{Name: name, Status: Pass, Detail: detail} }

func skip(name, detail string) Check { return Check{Name: name, Status: Skip, Detail: detail} }

func fail(name, detail, hint string) Check {
	return Check{Name: name, Status: Fail, Detail: detail, Hint: hint}
}
