package cli

import (
	"strings"

	"github.com/l4ci/rota/internal/roundcfg"
)

// applyGates declares each verb's pre-verb gate policy on the tree. A group's
// policy covers the verbs under it unless a verb or sub-group sets its own, so
// a new verb inherits the policy of its group. A path missing from the tree is
// a programming error, so it panics rather than leave a policy unapplied.
func applyGates(root *Command) {
	// Bare rota is the orchestrator launcher: with an old work.* round key in
	// the config it refuses like a round verb. The root's policy is its own and
	// is not inherited by the verbs under it.
	root.Gates.RoundKeys = RoundKeysRefuse
	for _, d := range gateDecls {
		for _, p := range d.paths {
			d.apply(&gateTarget(root, p).Gates)
		}
	}
	for class, paths := range fenceDecls {
		for _, p := range paths {
			gateTarget(root, p).Gates.Fence = class
		}
	}
}

func gateTarget(root *Command, path string) *Command {
	cmd := findVerb(root, strings.Fields(path))
	if cmd == nil || cmd == root {
		panic("gate declaration names " + path + ", which is not in the tree")
	}
	return cmd
}

// gateDecls are the policies other than the support fence.
var gateDecls = []struct {
	paths []string
	apply func(*Gates)
}{
	// Run on a project that still holds .hv/: the migration itself and doctor
	// (which reports it), the verbs that never read project state and the hooks
	// that must not fail a Claude session.
	{[]string{"migrate hv", "doctor", "version", "update", "skills", "hook", "statusline"}, func(g *Gates) { g.LegacyExempt = true }},
	// Read the round-only keys: with an old work.* name still in the config
	// they refuse (exit 4) rather than run on a key nothing reads any more
	// (#1097 D4).
	{[]string{"round", "worker", "doctor", "orchestrate", "keepalive", "layout", "limit"}, func(g *Gates) { g.RoundKeys = RoundKeysRefuse }},
	// Neither refuse nor warn: the ones that migrate or edit the config, and
	// those that must stay silent (hooks, the statusline) or never read project
	// state. `worker prompt-check` is the Codex prompt hook, which reads no
	// config and must not block a worker's prompt. `worker done|inbox|send|reply`
	// run from a worker's own worktree, whose tracked config.json can predate
	// the rename: a refusal there would send the worker to rewrite tracked
	// config on its branch (#1163). They never read the worktree's config.
	// `worker done` does read round.accounts (for the ledger headroom), but
	// through the pool root, the main checkout that holds the slot registry
	// (returnRoot), so the old names in the worktree never reach it.
	{[]string{"migrate", "config", "init", "update", "setup", "hook", "statusline", "version", "skills",
		"worker prompt-check", "worker done", "worker inbox", "worker send", "worker reply"}, func(g *Gates) { g.RoundKeys = RoundKeysQuiet }},
	// May run with round.role in the tracked config, which they are there to fix.
	{[]string{"config", "migrate"}, func(g *Gates) { g.TrackedRoleExempt = true }},
	// Refused for an unnamed support machine before any round verb reads or
	// claims issues; config verbs stay usable to repair the name.
	{[]string{"round"}, func(g *Gates) { g.RoundMachine = true }},
}

// fenceDecls is how the support fence treats each verb: a support machine
// (round.role support) never merges and never writes tracked content that only
// the main orchestrator commits. A support round runs on an issue backend only
// (D3), so a verb that writes the tracker there is allowed.
var fenceDecls = map[roundcfg.FenceClass][]string{
	// Refused before they run on a support machine: merge, runs an arbitrary
	// command, tracked .rota/ writes, tracked instruction files that main
	// regenerates and commits, the main-only work split and release.
	roundcfg.FenceRefused: {
		"backlog archive", "backlog backfill", "block", "config fill", "decisions add", "decisions amend",
		"decisions retire", "decisions supersede", "glossary import", "glossary write", "id", "init", "instructions",
		"knowledge add", "knowledge amend", "knowledge contradiction add", "knowledge contradiction clear",
		"knowledge rename-topic", "knowledge replace", "knowledge rm", "knowledge split", "knowledge tier fold",
		"knowledge tier set", "map index", "migrate", "milestone add", "milestone index", "milestone overview",
		"milestone put", "milestone status", "prompts adopt", "prompts restore", "prompts save", "qa index",
		"refactor reset", "release bump", "release changelog", "release close-milestone", "release publish",
		"release push", "review probe", "round support disable", "round support enable", "round support offer",
		"ship merge", "ship pr-merge", "ship undo", "spike add", "spike finish", "test lock", "worker gate",
		"worker train",
	},
	// Fenced inside the verb, which knows what it writes: config set and config
	// edit refuse a tracked key on a support machine and allow --global and
	// round.role (config.local.json); agents write skips the tracked .claude and
	// .opencode files and writes the gitignored .codex ones; knowledge harvest
	// lists freely and refuses --mark, which writes a tracker comment main owns;
	// setup's support path writes config.local.json only, and its main path
	// refuses the initialized clone a support machine runs in (#1774).
	roundcfg.FenceInVerb: {
		"agents", "config edit", "config set", "knowledge harvest", "setup",
	},
	// Run on a support machine: reads, writers of the tracker, of gitignored
	// per-machine state, and of files outside the repo.
	roundcfg.FenceAllowed: {
		"backlog drift", "backlog ids", "backlog list", "backlog milestones", "backlog stale", "bugreport", "change",
		"config check", "config save-global", "config show", "connect", "debug", "decisions check",
		"decisions query", "decisions stats", "decisions topics", "design", "doctor", "gate", "git", "glossary read",
		"hook", "init check", "issues", "item", "keepalive", "knowledge contradiction has",
		"knowledge contradiction list", "knowledge hit", "knowledge query", "knowledge stats", "knowledge tier get",
		"knowledge tier list", "knowledge topics", "labels", "layout", "limit", "map query", "map stats",
		"milestone active", "milestone check", "milestone list", "milestone show", "orchestrate", "plan", "projects",
		"prompts list", "prompts show", "proof", "qa query", "reap", "receipt", "refactor age", "refactor targets",
		"release host", "release milestone-check", "release notes", "release pending", "release version", "repo",
		"review brief", "review cleanup", "review depth", "review package", "review prepare", "review queue",
		"review scaffolding", "review scope", "round accept", "round architecture", "round assign", "round bounce",
		"round candidates", "round escalate", "round freeze", "round pick", "round reclaim", "round reconcile",
		"round report", "round return", "round review-relay", "round scope", "round start", "round status",
		"round summary", "round support return", "round tick", "round transfer", "round wait", "round watch",
		"round wind-down", "ship body", "ship finish", "ship pr", "ship strategy", "skills", "spike list",
		"spike show", "status", "statusline", "summary", "test flakes", "test ledger", "test run", "test-audit",
		"tracker", "update", "verdict", "version", "work", "worker account", "worker adopt", "worker dispatch",
		"worker done", "worker inbox", "worker poll", "worker pool", "worker prompt-check", "worker reply",
		"worker reset", "worker send", "worker session",
	},
}
