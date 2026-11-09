package cli

import (
	"strings"

	"github.com/l4ci/rota/internal/roundcfg"
)

// applyGates declares each verb's pre-verb gate policy on the tree. A group's
// policy covers the verbs under it unless a verb or sub-group sets its own,
// except the fence class, which each verb declares itself: a new verb must be
// classified, never silently allowed on a support machine. A path missing from the tree is
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
	declared := map[string]bool{}
	for class, paths := range fenceDecls {
		for _, p := range paths {
			if declared[p] {
				panic("fence declaration names " + p + " twice")
			}
			declared[p] = true
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
	// Run on a project that still holds the legacy state directory: the migration itself and doctor
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
	// Run with a corrupt project or local config, each for its own reason:
	// config and migrate edit it to repair it; init refuses a bad file on its
	// own terms; setup, version, update and statusline never load it; hook
	// loads it and degrades silently, since a hook must not break the session;
	// skills reads round.accounts and runs on defaults; worker prompt-check is
	// a Codex hook that only verifies a signature.
	{[]string{"config", "migrate", "init", "setup", "version", "update", "skills", "hook", "statusline",
		"worker prompt-check"}, func(g *Gates) { g.ConfigExempt = true }},
	// Refused for an unnamed support machine before any round verb reads or
	// claims issues; config verbs stay usable to repair the name.
	{[]string{"round"}, func(g *Gates) { g.RoundMachine = true }},
}

// fenceDecls is how the support fence treats each verb: a support machine
// (round.role support) never merges and never writes tracked content that only
// the main orchestrator commits. A support round runs on an issue backend only
// (D3), so a verb that writes the tracker there is allowed. Every verb is
// listed once, under its own path; a path in two classes panics.
var fenceDecls = map[roundcfg.FenceClass][]string{
	// Refused before they run on a support machine: merge, runs an arbitrary
	// command, tracked .rota/ writes, tracked instruction files that main
	// regenerates and commits, the main-only work split and release.
	roundcfg.FenceRefused: {
		"worker gate", "worker train", "worker train cancel", "ship merge", "ship pr-merge", "ship undo",
		"test lock", "review probe", "prompts save", "prompts adopt", "prompts restore", "decisions supersede",
		"decisions retire", "decisions add", "decisions amend", "knowledge add", "knowledge amend",
		"knowledge rename-topic", "knowledge replace", "knowledge rm", "knowledge split", "knowledge tier fold",
		"knowledge tier set", "knowledge contradiction add", "knowledge contradiction clear", "glossary write",
		"glossary import", "map index", "qa index", "milestone add", "milestone index", "milestone overview",
		"milestone put", "milestone status", "backlog archive", "backlog backfill", "config fill", "id next",
		"refactor reset", "spike add", "spike finish", "init", "init umbrella", "migrate labels", "migrate config",
		"migrate external-knowledge", "migrate hv", "migrate issues", "migrate milestone-files",
		"migrate milestones", "migrate source", "block", "instructions init", "round support offer",
		"round support enable", "round support disable", "release bump", "release changelog",
		"release close-milestone", "release publish", "release push",
	},
	// Fenced inside the verb, which knows what it writes: config set and config
	// edit refuse a tracked key on a support machine and allow --global and
	// round.role (config.local.json); agents write skips the tracked .claude and
	// .opencode files and writes the gitignored .codex ones; knowledge harvest
	// lists freely and refuses --mark, which writes a tracker comment main owns;
	// setup's support path writes config.local.json only, and its main path
	// refuses the initialized clone a support machine runs in (#1774).
	roundcfg.FenceInVerb: {
		"config set", "config edit", "knowledge harvest", "setup", "agents write",
	},
	// Run on a support machine: reads, writers of the tracker, of gitignored
	// per-machine state, and of files outside the repo.
	roundcfg.FenceAllowed: {
		"round support return", "prompts list", "prompts show", "backlog drift", "backlog ids", "backlog list",
		"backlog milestones", "backlog stale", "config check", "config show", "connect list", "connect test",
		"debug counter show", "debug counter summary", "debug session list", "debug session show", "decisions check",
		"decisions query", "decisions stats", "decisions topics", "design list", "design show", "doctor",
		"gate list", "git base", "git branch", "git guard clean", "git guard feature-branch", "git worktree-path",
		"glossary read", "init check", "issues imported", "issues provider", "item comment list", "item field get",
		"item field list", "item note show", "item show", "item template", "keepalive status", "layout",
		"limit status", "knowledge contradiction has", "knowledge contradiction list", "knowledge query",
		"knowledge stats", "knowledge tier get", "knowledge tier list", "knowledge topics", "map query", "map stats",
		"milestone active", "milestone list", "milestone show", "plan check", "plan list", "plan rename-check",
		"plan show", "plan uncertain", "plan validate-docs", "projects", "proof show", "qa query", "receipt show",
		"refactor age", "refactor targets", "change classify", "release host", "release milestone-check",
		"release notes", "release pending", "release version", "repo resolve", "repo umbrella", "repo which",
		"review brief", "review depth", "review package", "review queue", "review scaffolding", "review scope",
		"round candidates", "round status", "round summary", "round watch", "round wait", "ship body",
		"ship strategy", "skills get", "skills links", "skills list", "skills status", "spike list", "spike show",
		"status show", "statusline dump", "summary", "test flakes", "test ledger check", "test run", "test-audit",
		"tracker suggest-upstream", "update", "verdict show", "version", "worker account list", "worker pool list",
		"worker prompt-check", "worker session check", "design add", "design amend", "design put", "design rm",
		"plan add", "plan pass", "plan put", "plan rm", "proof add", "proof record", "item claim",
		"item comment add", "item complete", "item create", "item field set", "item note add", "item note rm",
		"item ready", "item release", "item reopen", "item rm", "item section set", "item shipped", "item state",
		"issues close", "issues label", "labels sync", "tracker call", "milestone check", "ship pr", "round freeze",
		"round accept", "round architecture", "round assign", "round bounce", "round escalate check",
		"round escalate send", "round pick", "round reclaim", "round reconcile", "round report", "round return",
		"round review-relay", "round scope", "round start", "round tick", "round transfer", "round wind-down",
		"worker account assign", "worker account pick", "worker adopt", "worker dispatch", "worker done",
		"worker inbox", "worker poll", "worker pool init", "worker pool reap", "worker reply", "worker reset",
		"worker send", "worker session ensure", "reap", "orchestrate", "keepalive run", "layout split",
		"layout tabs", "limit watch", "knowledge hit", "bugreport add", "bugreport dismiss", "bugreport draft",
		"bugreport duplicates", "bugreport file", "bugreport list", "verdict add", "verdict post", "verdict route",
		"status add", "status handoff", "status rm", "ship finish", "work start", "debug counter clear",
		"debug counter fail", "debug counter inc-cycle", "debug counter init", "debug counter pass",
		"debug counter record-attempt", "debug reset", "debug session put", "debug session rm", "debug verdict",
		"debug work-fix attempt", "debug work-fix pass", "debug work-fix reset", "repo add", "repo rm",
		"review prepare", "review cleanup", "config save-global", "connect add", "connect remove",
		"projects cleanup", "projects remove", "hook guard", "hook install", "hook prompt", "hook session-start",
		"hook stop", "hook uninstall", "skills install", "skills uninstall", "skills update",
	},
}
